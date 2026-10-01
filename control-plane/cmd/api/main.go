package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"umpp/control-plane/internal/api"
	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/metrics"
	"umpp/control-plane/internal/service"
	"umpp/control-plane/internal/service/alerts"
	acmeclient "umpp/control-plane/internal/service/cert/acme"
	"umpp/control-plane/internal/service/proxy"
	"umpp/control-plane/internal/service/proxy/nps"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "umpp-api:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("umpp-api", flag.ContinueOnError)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML configuration")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.Log)
	handle, err := db.Open(cfg.Database, cfg.Log.Level)
	if err != nil {
		return err
	}
	if err := db.AutoMigrate(handle); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := service.Bootstrap(ctx, handle, cfg.Bootstrap); err != nil {
		return err
	}
	manager, err := auth.NewManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)
	if err != nil {
		return err
	}
	nodeService := service.NewNodeService(handle, cfg.Node.HeartbeatTimeout)
	sweeper := service.NewNodeSweeper(handle, cfg.Node.HeartbeatTimeout, logger)
	go sweeper.Start(ctx, 15*time.Second)

	promMetrics := metrics.New(handle)
	var proxyProvider proxy.Provider
	var builtinProvider *proxy.Builtin
	switch cfg.Proxy.Kind {
	case "builtin":
		builtinProvider = proxy.NewBuiltin(handle, manager, logger, promMetrics)
		proxyProvider = builtinProvider
	case "nps":
		proxyProvider = nps.New(handle, nps.Options{
			ConfigPath:     cfg.Proxy.NPS.ConfigPath,
			BinaryPath:     cfg.Proxy.NPS.BinaryPath,
			PIDFile:        cfg.Proxy.NPS.PIDFile,
			ReloadStrategy: cfg.Proxy.NPS.ReloadStrategy,
		}, logger, promMetrics)
	default:
		return fmt.Errorf("unsupported proxy provider %q", cfg.Proxy.Kind)
	}
	if err := proxyProvider.Reload(ctx); err != nil {
		return fmt.Errorf("initial proxy provider reload: %w", err)
	}
	challengeStore := acmeclient.NewChallengeStore(15 * time.Minute)
	acmeTransport := acmeclient.NewX509Transport(acmeclient.Config{
		Enabled:      cfg.ACME.Enabled,
		DirectoryURL: cfg.ACME.DirectoryURL,
		Email:        cfg.ACME.Email,
		Challenge:    cfg.ACME.Challenge,
		HTTPPort:     cfg.ACME.HTTPPort,
		KeyType:      cfg.ACME.KeyType,
		AgreeTOS:     cfg.ACME.AgreeTOS,
		CACertFile:   cfg.ACME.CACertFile,
	})
	acmeClient := acmeclient.New(acmeclient.Config{
		Enabled:      cfg.ACME.Enabled,
		DirectoryURL: cfg.ACME.DirectoryURL,
		Email:        cfg.ACME.Email,
		Challenge:    cfg.ACME.Challenge,
		HTTPPort:     cfg.ACME.HTTPPort,
		KeyType:      cfg.ACME.KeyType,
		AgreeTOS:     cfg.ACME.AgreeTOS,
		CACertFile:   cfg.ACME.CACertFile,
	}, acmeTransport)
	handler := api.NewWithProxy(handle, manager, nodeService, promMetrics, logger, version, proxyProvider, api.ProxyOptions{
		Enabled: cfg.Proxy.Enabled,
		Kind:    cfg.Proxy.Kind,
		Listen:  cfg.Proxy.Listen,
		TLS: api.ProxyTLSOptions{
			Enabled:    cfg.Proxy.TLS.Enabled,
			Listen:     cfg.Proxy.TLS.Listen,
			MinVersion: cfg.Proxy.TLS.MinVersion,
		},
		ACME: acmeClient,
		ACMEOptions: service.ACMEOptions{
			AutoRenew:       cfg.ACME.AutoRenew,
			RenewBefore:     time.Duration(cfg.ACME.RenewBeforeDays) * 24 * time.Hour,
			CheckInterval:   cfg.ACME.CheckInterval,
			ChallengeSolver: challengeStore,
		},
		ChallengeHandler: challengeStore.Handler(),
		RateLimit: api.RateLimitOptions{
			Enabled: cfg.RateLimit.Enabled,
			RPS:     cfg.RateLimit.RPS,
			Burst:   cfg.RateLimit.Burst,
		},
		Alerts: alertOptions(cfg),
	})
	go handler.StartCertificateLifecycle(ctx)
	go handler.StartAlertEvaluation(ctx)
	if cfg.ACME.Enabled {
		go challengeStore.RunCleanup(ctx, time.Minute)
	}
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("UMPP API listening", "address", server.Addr, "version", version)
		serverErrors <- server.ListenAndServe()
	}()

	var challengeServer *http.Server
	challengeErrors := make(chan error, 1)
	if cfg.ACME.Enabled && cfg.ACME.Challenge == acmeclient.ChallengeHTTP01 {
		challengeServer = &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.ACME.HTTPPort),
			Handler:           challengeStore.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Info("UMPP ACME HTTP-01 challenge listening", "address", challengeServer.Addr)
			challengeErrors <- challengeServer.ListenAndServe()
		}()
	}

	var proxyServer *http.Server
	proxyErrors := make(chan error, 1)
	if cfg.Proxy.Enabled && builtinProvider != nil {
		proxyServer = &http.Server{
			Addr:              cfg.Proxy.Listen,
			Handler:           builtinProvider,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			logger.Info("UMPP proxy listening", "address", proxyServer.Addr, "kind", cfg.Proxy.Kind)
			proxyErrors <- proxyServer.ListenAndServe()
		}()
	}

	var tlsServer *http.Server
	tlsErrors := make(chan error, 1)
	if cfg.Proxy.TLS.Enabled {
		if builtinProvider == nil {
			return fmt.Errorf("proxy.tls.enabled requires proxy.kind=builtin")
		}
		tlsServer = &http.Server{
			Addr:              cfg.Proxy.TLS.Listen,
			Handler:           builtinProvider,
			TLSConfig:         builtinProvider.TLSConfig(cfg.Proxy.TLS.MinVersion),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			logger.Info("UMPP proxy TLS listening", "address", tlsServer.Addr, "min_version", cfg.Proxy.TLS.MinVersion)
			tlsErrors <- tlsServer.ListenAndServeTLS("", "")
		}()
	}

	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	defer signal.Stop(reloadSignals)
	shutdown := func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, listener := range []*http.Server{tlsServer, proxyServer, challengeServer, server} {
			if listener == nil {
				continue
			}
			if err := listener.Shutdown(shutdownCtx); err != nil {
				return err
			}
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return shutdown()
		case <-reloadSignals:
			if reloadErr := proxyProvider.Reload(context.Background()); reloadErr != nil {
				logger.Warn("proxy reload failed", "error", reloadErr)
			}
		case err := <-serverErrors:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case err := <-proxyErrors:
			if errors.Is(err, http.ErrServerClosed) {
				continue
			}
			return err
		case err := <-tlsErrors:
			if errors.Is(err, http.ErrServerClosed) {
				continue
			}
			return err
		case err := <-challengeErrors:
			if errors.Is(err, http.ErrServerClosed) {
				continue
			}
			return err
		}
	}
}

func newLogger(cfg config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, options)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, options)
	}
	return slog.New(handler)
}

func alertOptions(cfg config.Config) alerts.Options {
	return alerts.Options{
		EvaluationInterval:    cfg.Alerts.EvaluationInterval,
		NodeOfflineAfter:      cfg.Alerts.NodeOfflineAfter,
		CertificateExpiringIn: cfg.Alerts.CertificateExpiringIn,
		CertificateCriticalIn: cfg.Alerts.CertificateCriticalIn,
		P2PSuccessRateMinimum: cfg.Alerts.P2PSuccessRateMinimum,
		RelaySpikeMultiplier:  cfg.Alerts.RelaySpikeMultiplier,
		RelayBaselineWindow:   cfg.Alerts.RelayBaselineWindow,
		ResolvedRetention:     cfg.Alerts.ResolvedRetention,
		WebhookURL:            cfg.Alerts.WebhookURL,
		WebhookTimeout:        cfg.Alerts.WebhookTimeout,
		WebhookRetries:        cfg.Alerts.WebhookRetries,
	}
}
