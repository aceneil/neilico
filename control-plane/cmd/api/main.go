package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/service"
	"neilico/control-plane/internal/service/alerts"
	acmeclient "neilico/control-plane/internal/service/cert/acme"
	"neilico/control-plane/internal/service/pki"
	"neilico/control-plane/internal/service/proxy"
	"neilico/control-plane/internal/service/proxy/nps"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "neilico-api:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("neilico-api", flag.ContinueOnError)
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
		Enabled:       cfg.Proxy.Enabled,
		Kind:          cfg.Proxy.Kind,
		Listen:        cfg.Proxy.Listen,
		StreamPortMin: cfg.Proxy.StreamPortMin,
		StreamPortMax: cfg.Proxy.StreamPortMax,
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
		Dashboard: api.DashboardOptions{
			Dir: cfg.Server.DashboardDir,
			SPA: cfg.Server.DashboardSPA,
		},
		// ⚠️ 这两个必须显式传：漏掉 Downloads 会让 /downloads/* 全部 404
		//（downloadsDir 为空 → filepath.Join("", name) 变成相对路径），
		// 而单测自己构造 options 传了临时目录，所以只有真机部署才暴露。
		Downloads: api.DownloadsOptions{
			Dir: cfg.Downloads.Dir,
		},
		Enroll: api.EnrollOptions{
			SigningKey: cfg.Enroll.SigningKey,
			PublicURL:  cfg.Enroll.PublicURL,
			AgentImage: cfg.Enroll.AgentImage,
		},
		RateLimit: api.RateLimitOptions{
			Enabled: cfg.RateLimit.Enabled,
			RPS:     cfg.RateLimit.RPS,
			Burst:   cfg.RateLimit.Burst,
		},
		Alerts: alertOptions(cfg),
		Bootstrap: api.BootstrapOptions{
			EnvFile:       cfg.Bootstrap.EnvFile,
			DefaultTenant: cfg.Bootstrap.DefaultTenant,
		},
		PKI: api.PKIOptions{
			Enabled:         cfg.PKI.Enabled,
			CommonName:      cfg.PKI.CACertName,
			ServerHosts:     cfg.PKI.ServerHosts,
			ServerCertDays:  cfg.PKI.ServerCertDays,
			NodeCertDays:    cfg.PKI.NodeCertDays,
			RenewBeforeDays: cfg.PKI.RenewBeforeDays,
		},
	})
	if cfg.Downloads.Dir == "" {
		logger.Warn("agent downloads are disabled: downloads.dir is empty, /downloads/* will return 404")
	} else if info, err := os.Stat(cfg.Downloads.Dir); err != nil || !info.IsDir() {
		logger.Warn("agent downloads directory is unusable, /downloads/* will return 404",
			"dir", cfg.Downloads.Dir, "error", err)
	} else {
		logger.Info("agent downloads ready", "dir", cfg.Downloads.Dir)
	}
	go handler.StartCertificateLifecycle(ctx)
	go handler.StartAlertEvaluation(ctx)
	if cfg.ACME.Enabled {
		go challengeStore.RunCleanup(ctx, time.Minute)
	}
	serverHandler := http.Handler(handler)
	// 启动时把「端口转发」规则装载进转发引擎（保存即时生效在 API 层负责）。
	handler.ReconcileStreams(ctx)
	serverTLSConfig, err := apiServerTLSConfig(cfg, handler, promMetrics)
	if err != nil {
		return err
	}
	if serverTLSConfig != nil {
		serverHandler = requireClientCertificate(handler, cfg.Server.TLS.ClientAuth, promMetrics)
	}
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           serverHandler,
		TLSConfig:         serverTLSConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		mode := "http"
		var serveErr error
		if serverTLSConfig != nil {
			mode = "https"
			logger.Info("NEILICO API TLS listening", "address", server.Addr, "version", version,
				"min_version", cfg.Server.TLS.MinVersion, "client_auth", cfg.Server.TLS.ClientAuth)
			serveErr = server.ListenAndServeTLS("", "")
		} else {
			logger.Info("NEILICO API listening", "address", server.Addr, "version", version)
			serveErr = server.ListenAndServe()
		}
		_ = mode
		serverErrors <- serveErr
	}()

	var redirectServer *http.Server
	redirectErrors := make(chan error, 1)
	if serverTLSConfig != nil && cfg.Server.TLS.RedirectHTTP {
		redirectHandler := redirectAPI(handler, fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.TLS.HTTPPort), cfg.Server.Port)
		redirectServer = &http.Server{
			Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.TLS.HTTPPort),
			Handler:           redirectHandler,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Info("NEILICO API HTTP redirect listening", "address", redirectServer.Addr, "https_port", cfg.Server.Port)
			redirectErrors <- redirectServer.ListenAndServe()
		}()
	}

	var challengeServer *http.Server
	challengeErrors := make(chan error, 1)
	if cfg.ACME.Enabled && cfg.ACME.Challenge == acmeclient.ChallengeHTTP01 {
		challengeServer = &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.ACME.HTTPPort),
			Handler:           challengeStore.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Info("NEILICO ACME HTTP-01 challenge listening", "address", challengeServer.Addr)
			challengeErrors <- challengeServer.ListenAndServe()
		}()
	}

	var proxyServer *http.Server
	proxyErrors := make(chan error, 1)
	if cfg.Proxy.Enabled && builtinProvider != nil {
		proxyServer = &http.Server{
			Addr:              cfg.Proxy.Listen,
			Handler:           proxyHTTPHandler(builtinProvider, cfg.Proxy.TLS),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			logger.Info("NEILICO proxy listening", "address", proxyServer.Addr, "kind", cfg.Proxy.Kind)
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
			Handler:           proxyHTTPHandler(builtinProvider, cfg.Proxy.TLS),
			TLSConfig:         builtinProvider.TLSConfig(cfg.Proxy.TLS.MinVersion),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			logger.Info("NEILICO proxy TLS listening", "address", tlsServer.Addr, "min_version", cfg.Proxy.TLS.MinVersion)
			tlsErrors <- tlsServer.ListenAndServeTLS("", "")
		}()
	}

	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	defer signal.Stop(reloadSignals)
	shutdown := func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, listener := range []*http.Server{tlsServer, proxyServer, challengeServer, redirectServer, server} {
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
		case err := <-redirectErrors:
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

func apiServerTLSConfig(cfg config.Config, handler *api.Handler, observer *metrics.Metrics) (*tls.Config, error) {
	if !cfg.Server.TLS.Enabled {
		return nil, nil
	}
	minVersion := uint16(tls.VersionTLS12)
	if cfg.Server.TLS.MinVersion == "1.3" {
		minVersion = tls.VersionTLS13
	}
	result := &tls.Config{MinVersion: minVersion}
	var pkiService *pki.Service
	if handler != nil {
		pkiService = handler.PKI()
	}
	if cfg.Server.TLS.CertFile != "" {
		certificate, err := tls.LoadX509KeyPair(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load server.tls certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{certificate}
	} else {
		if !cfg.PKI.Enabled || pkiService == nil {
			return nil, errors.New("server.tls.enabled requires cert_file/key_file or pki.enabled=true")
		}
		if _, err := pkiService.EnsureCA(); err != nil {
			return nil, fmt.Errorf("initialize PKI for server TLS: %w", err)
		}
		result.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			certificate, certErr := pkiService.ServerCertificate(cfg.PKI.ServerHosts)
			if certErr != nil {
				return nil, certErr
			}
			return &certificate, nil
		}
	}
	clientCAs, err := serverClientCAs(cfg, pkiService)
	if err != nil {
		return nil, err
	}
	result.ClientCAs = clientCAs
	switch cfg.Server.TLS.ClientAuth {
	case "request":
		result.ClientAuth = tls.VerifyClientCertIfGiven
	case "require":
		if clientCAs == nil {
			return nil, errors.New("server.tls.client_auth=require needs pki.enabled=true or server.tls.client_ca_file")
		}
		// Health, metrics, and the public CA bootstrap endpoint need to work
		// without a client certificate; HTTP middleware enforces the exception set.
		result.ClientAuth = tls.VerifyClientCertIfGiven
	default:
		result.ClientAuth = tls.NoClientCert
	}
	result.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) > 0 && result.ClientCAs != nil {
			if _, verifyErr := state.PeerCertificates[0].Verify(x509.VerifyOptions{
				Roots:     result.ClientCAs,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}); verifyErr != nil {
				observer.ObserveTLSHandshake("failure", "api")
				return fmt.Errorf("verify client certificate: %w", verifyErr)
			}
		}
		result := "success"
		if len(state.PeerCertificates) == 0 && cfg.Server.TLS.ClientAuth == "request" {
			result = "unauthenticated"
		}
		observer.ObserveTLSHandshake(result, "api")
		return nil
	}
	return result, nil
}

func serverClientCAs(cfg config.Config, pkiService *pki.Service) (*x509.CertPool, error) {
	if cfg.Server.TLS.ClientAuth == "none" {
		return nil, nil
	}
	if cfg.Server.TLS.ClientCAFile != "" {
		data, err := os.ReadFile(cfg.Server.TLS.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read server.tls.client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("server.tls.client_ca_file contains no certificates")
		}
		return pool, nil
	}
	if cfg.PKI.Enabled && pkiService != nil {
		return pkiService.ClientCACertPool()
	}
	return nil, nil
}

func requireClientCertificate(next http.Handler, mode string, observer *metrics.Metrics) http.Handler {
	if mode != "require" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bootstrap is deliberately narrow: public trust-anchor download and
		// authenticated enrollment must exist before the first client cert does.
		// Login, node registration, and POST node mTLS still run all authorization
		// and audit middleware below.
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || r.URL.Path == "/api/v1/pki/ca" ||
			r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/refresh" ||
			strings.HasPrefix(r.URL.Path, "/api/v1/setup/") ||
			r.URL.Path == "/api/v1/nodes/register" ||
			(r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/mtls")) {
			next.ServeHTTP(w, r)
			return
		}
		if len(r.TLS.VerifiedChains) == 0 {
			observer.ObserveTLSHandshake("client_certificate_required", "api")
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func redirectAPI(next http.Handler, listenAddress string, tlsPort int) http.Handler {
	_, port, _ := net.SplitHostPort(listenAddress)
	_ = port
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if strings.Contains(host, ":") {
			host, _, _ = net.SplitHostPort(host)
		}
		status := http.StatusMovedPermanently
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			status = http.StatusPermanentRedirect
		}
		w.Header().Set("Location", fmt.Sprintf("https://%s:%d%s", host, tlsPort, r.URL.RequestURI()))
		w.WriteHeader(status)
	})
}

func proxyHTTPHandler(next http.Handler, options config.ProxyTLS) http.Handler {
	result := proxy.SecurityHeaders(next, options.HSTSMaxAge)
	if options.Enabled && options.RedirectHTTP {
		result = proxy.RedirectHTTP(result, options.Listen)
	}
	return result
}
