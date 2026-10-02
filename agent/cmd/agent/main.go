package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"umpp/agent/internal/client"
	"umpp/agent/internal/config"
	"umpp/agent/internal/heartbeat"
	"umpp/agent/internal/mesh"
	agentmetrics "umpp/agent/internal/metrics"
	"umpp/agent/internal/route"
	"umpp/agent/internal/state"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "umpp-agent:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("umpp-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "configs/agent.example.yaml", "path to YAML configuration")
	dryRun := flags.Bool("dry-run", false, "print network configuration and commands without writing")
	forceRegister := flags.Bool("force-register", false, "register a new node identity even if state exists")
	statePath := flags.String("state", "", "override state.json path")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *statePath != "" {
		cfg.StatePath = *statePath
	}
	logger := newLogger(cfg.Log.Level, stderr)
	warnInsecureTLS(cfg.TLS.InsecureSkipVerify, logger, stderr)
	metrics := agentmetrics.New()
	metrics.SetDryRun(*dryRun || !hasNetAdmin())
	if metrics.DryRun() {
		logger.Info("network application running in dry-run; no system writes will be executed", "requested", *dryRun, "cap_net_admin", hasNetAdmin())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	apiClient, err := client.NewWithTLS(cfg.Server, cfg.Token, client.TLSOptions{
		CAFile:             cfg.TLS.CAFile,
		ClientCertFile:     cfg.TLS.ClientCertFile,
		ClientKeyFile:      cfg.TLS.ClientKeyFile,
		ServerName:         cfg.TLS.ServerName,
		InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
	})
	if err != nil {
		return err
	}
	identity, err := ensureIdentity(ctx, cfg, apiClient, *forceRegister, logger)
	if err != nil {
		return err
	}
	apiClient.SetToken(identity.AgentToken)
	logger.Info("agent identity ready", "node_id", identity.NodeID)

	executor := route.ExecExecutor{}
	applier := mesh.NewApplier(metrics.DryRun(), executor, "", stdout, logger)
	routeManager := route.NewManager(executor, route.Options{
		Interface:         cfg.Mesh.Interface,
		AllowForwarding:   cfg.Mesh.AllowForwarding,
		ExternalInterface: cfg.Mesh.ExternalInterface,
		MeshCIDR:          meshCIDR(identity.NodeID, cfg),
	})
	reconciler := mesh.NewReconciler(mesh.ReconcilerOptions{
		StatePath:       cfg.StatePath,
		Applier:         applier,
		Routes:          routeManager,
		Metrics:         metrics,
		Logger:          logger,
		Output:          stdout,
		DryRun:          metrics.DryRun(),
		AllowProxy:      cfg.Proxy.Enabled,
		AllowForwarding: cfg.Mesh.AllowForwarding,
		Interface:       cfg.Mesh.Interface,
		MTU:             cfg.Mesh.MTU,
		ListenPort:      cfg.Mesh.ListenPort,
	})

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		heartbeat.Run(ctx, time.Duration(cfg.HeartbeatInterval), func(ctx context.Context) (time.Duration, error) {
			response, err := apiClient.Heartbeat(ctx, identity.NodeID, version)
			if err != nil {
				return 0, err
			}
			return time.Duration(response.NextHeartbeatSeconds) * time.Second, nil
		}, metrics, logger)
	}()
	go func() { defer wg.Done(); runConfigLoop(ctx, cfg, identity, apiClient, reconciler, metrics, logger) }()
	go func() { defer wg.Done(); runEndpointLoop(ctx, cfg, identity, apiClient, logger) }()

	if cfg.Metrics.Enabled {
		server := &http.Server{Addr: cfg.Metrics.Listen, Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics server failed", "error", err)
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
	}
	if cfg.Proxy.Enabled {
		logger.Info("NPS tunnel client pending integration", "server", cfg.Proxy.NPSServer)
		metrics.SetProxyPending(true)
	}
	traffic := &agentmetrics.TrafficReporter{
		Reader:    agentmetrics.SysfsReader{},
		Interface: cfg.Mesh.Interface,
		Client:    apiClient,
		NodeID:    identity.NodeID,
		Metrics:   metrics,
		Interval:  time.Minute,
	}
	wg.Add(1)
	go func() { defer wg.Done(); traffic.Run(ctx) }()

	<-ctx.Done()
	wg.Wait()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := reconciler.Cleanup(cleanupCtx, cfg.Mesh.CleanupOnExit); err != nil {
		logger.Warn("cleanup incomplete", "error", err)
	}
	return nil
}

func ensureIdentity(ctx context.Context, cfg config.Config, apiClient *client.Client, force bool, logger *slog.Logger) (state.State, error) {
	stored, exists, err := state.Load(cfg.StatePath)
	if err != nil {
		return state.State{}, err
	}
	if exists && !force {
		return stored, nil
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return state.State{}, errors.New("registration requires token configuration")
	}
	var registered state.State
	err = heartbeat.RetryUntil(ctx, func(ctx context.Context) error {
		response, err := apiClient.Register(ctx, client.RegisterRequest{
			Name:    cfg.Node.Name,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			Version: version,
			Tags:    cfg.Node.Tags,
		})
		if err != nil {
			return err
		}
		registered = state.State{
			NodeID:     response.NodeID,
			AgentToken: response.AgentToken,
			PrivateKey: response.PrivateKey,
			PublicKey:  response.PublicKey,
		}
		return state.Save(cfg.StatePath, registered)
	}, nil, logger)
	if err != nil {
		return state.State{}, err
	}
	logger.Info("node registered", "node_id", registered.NodeID)
	return registered, nil
}

func runConfigLoop(ctx context.Context, cfg config.Config, identity state.State, apiClient *client.Client, reconciler *mesh.Reconciler, observer *agentmetrics.Metrics, logger *slog.Logger) {
	backoff := heartbeat.NewBackoff(time.Second, 60*time.Second)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		current, exists, err := state.Load(cfg.StatePath)
		if err != nil {
			logger.Error("load state for config poll failed", "error", err)
		} else if exists {
			identity = current
		}
		err = pollOnce(ctx, identity, apiClient, reconciler, observer, logger)
		if err != nil {
			timer.Reset(backoff.Next())
			continue
		}
		backoff.Reset()
		timer.Reset(time.Duration(cfg.PollInterval))
	}
}

func pollOnce(ctx context.Context, identity state.State, apiClient *client.Client, reconciler *mesh.Reconciler, observer *agentmetrics.Metrics, logger *slog.Logger) error {
	result, err := apiClient.Config(ctx, identity.NodeID, identity.AppliedVersion)
	if err != nil {
		observer.ConfigPull("failure")
		logger.Warn("configuration pull failed", "error", err)
		return err
	}
	if result.NotModified {
		observer.ConfigPull("not_modified")
		return nil
	}
	if result.Delivery.Version <= identity.AppliedVersion {
		// M2b snapshots may return the requested historical version. Fetch the
		// latest snapshot explicitly before applying anything.
		latest, latestErr := apiClient.Config(ctx, identity.NodeID, 0)
		if latestErr != nil {
			observer.ConfigPull("failure")
			logger.Warn("configuration latest fetch failed", "error", latestErr)
			return latestErr
		}
		if latest.NotModified || latest.Delivery.Version <= identity.AppliedVersion {
			observer.ConfigPull("not_modified")
			return nil
		}
		result = latest
	}
	observer.ConfigPull("success")
	if err := reconciler.Reconcile(ctx, result.Delivery); err != nil {
		logger.Warn("configuration apply failed; retaining previous version", "version", result.Delivery.Version, "error", err)
		return err
	}
	return nil
}

func runEndpointLoop(ctx context.Context, cfg config.Config, identity state.State, apiClient *client.Client, logger *slog.Logger) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		endpoint := strings.TrimSpace(cfg.Mesh.PublicEndpoint)
		if endpoint == "" {
			endpoint = detectPublicEndpoint(ctx, cfg.Mesh.ListenPort)
		}
		if endpoint != "" {
			current, exists, err := state.Load(cfg.StatePath)
			if err == nil && exists {
				identity = current
				due := time.Since(identity.LastEndpointReport) >= 5*time.Minute
				if identity.LastEndpoint != endpoint || due {
					if err := apiClient.ReportEndpoint(ctx, identity.NodeID, endpoint); err != nil {
						logger.Warn("public endpoint report failed", "error", err)
					} else {
						identity.LastEndpoint = endpoint
						identity.LastEndpointReport = time.Now().UTC()
						if err := state.Save(cfg.StatePath, identity); err != nil {
							logger.Warn("save public endpoint state failed", "error", err)
						}
					}
				}
			}
		}
		timer.Reset(5 * time.Minute)
	}
}

func detectPublicEndpoint(ctx context.Context, listenPort int) string {
	detectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(detectCtx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return ""
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	data := make([]byte, 64)
	n, _ := io.ReadFull(response.Body, data)
	ip := strings.TrimSpace(string(data[:n]))
	if ip == "" || strings.ContainsAny(ip, " \t\r\n:/") {
		return ""
	}
	return fmt.Sprintf("%s:%d", ip, listenPort)
}

func meshCIDR(_ string, _ config.Config) string {
	// WireGuard overlay addressing is 100.64.0.0/10 (CGNAT), matching UMPP defaults.
	return "100.64.0.0/10"
}

func hasNetAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	const capability = uint64(1) << 12
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		var value uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), "%x", &value); err == nil {
			return value&capability != 0
		}
	}
	return false
}

func newLogger(level string, output io.Writer) *slog.Logger {
	var parsed slog.Level
	switch level {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: parsed}))
}

func warnInsecureTLS(enabled bool, logger *slog.Logger, stderr io.Writer) {
	if !enabled {
		return
	}
	logger.Error("SECURITY WARNING: TLS certificate verification is disabled for the control-plane connection")
	fmt.Fprintln(stderr, "*** SECURITY WARNING: tls.insecure_skip_verify=true disables control-plane certificate verification ***")
}
