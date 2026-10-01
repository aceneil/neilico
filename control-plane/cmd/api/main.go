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
	handler := api.NewWithProxy(handle, manager, nodeService, promMetrics, logger, version, proxyProvider, api.ProxyOptions{
		Enabled: cfg.Proxy.Enabled,
		Kind:    cfg.Proxy.Kind,
		Listen:  cfg.Proxy.Listen,
	})
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

	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	defer signal.Stop(reloadSignals)
	shutdown := func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if proxyServer != nil {
			if err := proxyServer.Shutdown(shutdownCtx); err != nil {
				return err
			}
		}
		return server.Shutdown(shutdownCtx)
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
