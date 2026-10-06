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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	agentcapabilities "neilico/agent/internal/capabilities"
	"neilico/agent/internal/client"
	"neilico/agent/internal/config"
	"neilico/agent/internal/heartbeat"
	"neilico/agent/internal/mesh"
	agentmetrics "neilico/agent/internal/metrics"
	"neilico/agent/internal/route"
	"neilico/agent/internal/state"
	"neilico/control-plane/pkg/capabilities"
	"neilico/control-plane/pkg/enrolltoken"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "neilico-agent:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "enroll" {
		return runEnroll(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	flags := flag.NewFlagSet("neilico-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to YAML configuration")
	dryRun := flags.Bool("dry-run", false, "print network configuration and commands without writing")
	forceRegister := flags.Bool("force-register", false, "register a new node identity even if state exists")
	statePath := flags.String("state", "", "override state.json path")
	stateDir := flags.String("state-dir", "", "override state directory")
	token := flags.String("token", "", "one-time enrollment token")
	tokenFile := flags.String("token-file", "", "file containing a one-time enrollment token")
	server := flags.String("server", "", "control-plane server URL override")
	nodeName := flags.String("name", "", "node name override")
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
	if err := applyEnrollFlags(&cfg, *token, *tokenFile, *server, *nodeName, *statePath, *stateDir); err != nil {
		return err
	}
	logger := newLogger(cfg.Log.Level, stderr)
	warnInsecureTLS(cfg.TLS.InsecureSkipVerify, logger, stderr)
	capabilityReport := agentcapabilities.Detect()
	logCapabilities(capabilityReport, logger)
	metrics := agentmetrics.New()
	metrics.SetDryRun(shouldDryRun(*dryRun, capabilityReport))
	if metrics.DryRun() {
		logger.Info("network application running in dry-run; no system writes will be executed", "requested", *dryRun, "cap_net_admin", hasNetAdmin(), "reason", capabilityReport.Reason)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	apiClient, err := newAPIClient(cfg)
	if err != nil {
		return err
	}
	identity, err := ensureIdentityWithCapabilities(ctx, cfg, apiClient, *forceRegister, capabilityReport, logger)
	if err != nil {
		return client.SafeError(err, cfg.EnrollToken, cfg.Token)
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
			response, err := apiClient.HeartbeatWithCapabilities(ctx, identity.NodeID, version, capabilitySnapshot())
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

func runEnroll(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("neilico-agent enroll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	token := flags.String("token", "", "one-time enrollment token (required)")
	tokenFile := flags.String("token-file", "", "file containing a one-time enrollment token")
	name := flags.String("name", "", "node name (defaults to hostname)")
	server := flags.String("server", "", "control-plane URL (defaults to token payload)")
	statePath := flags.String("state", "", "override state.json path")
	stateDir := flags.String("state-dir", "", "override state directory")
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
	cfg := config.Default()
	cfg.Token = ""
	cfg.Node.Name = ""
	if err := applyEnrollFlags(&cfg, *token, *tokenFile, *server, *name, *statePath, *stateDir); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.EnrollToken) == "" {
		return errors.New("enrollment token is required; pass --token or --token-file")
	}
	_, err := enrollAndSaveWithCapabilities(context.Background(), cfg, stdout, agentcapabilities.Detect())
	return err
}

func applyEnrollFlags(cfg *config.Config, token, tokenFile, server, nodeName, statePath, stateDir string) error {
	token = strings.TrimSpace(token)
	tokenFile = strings.TrimSpace(tokenFile)
	if token != "" && tokenFile != "" {
		return errors.New("--token and --token-file cannot be used together")
	}
	if token == "" && tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return fmt.Errorf("read enrollment token file: %w", err)
		}
		token = strings.TrimSpace(string(data))
		if token == "" {
			return errors.New("enrollment token file is empty")
		}
	}
	if token != "" {
		cfg.EnrollToken = token
	}
	if server = strings.TrimSpace(server); server != "" {
		cfg.Server = server
	}
	if nodeName = strings.TrimSpace(nodeName); nodeName != "" {
		cfg.Node.Name = nodeName
	}
	if statePath = strings.TrimSpace(statePath); statePath != "" {
		cfg.StatePath = statePath
	}
	if stateDir = strings.TrimSpace(stateDir); stateDir != "" {
		cfg.StatePath = filepath.Join(stateDir, "state.json")
	}
	if strings.TrimSpace(server) == "" {
		// 服务器地址的优先级：显式 --server > 配置文件里写明的非默认值 > 令牌载荷 > state.json > 默认值。
		//
		// 这里必须考虑 state：节点**首次 enroll 之后就不再需要令牌**了，凭据（含服务器地址）
		// 都在 state.json 里。早先的实现只看令牌，于是——
		//   · compose 文件里留着一个占位/过期的令牌 → 令牌无法解析 → **直接退出 → 容器无限重启**；
		//   · 就算绕过上一条，cfg.Server 仍是 config.Default() 里的占位地址，
		//     重启后的 agent 会打到错误的服务器。
		// 二者都会让一个"本来好好的" agent 因为文件里的一段废令牌而瘫痪。
		storedServer, hasStored := stateServer(cfg.StatePath)
		switch {
		case cfg.Server != "" && cfg.Server != config.Default().Server:
			// 配置文件显式指定了 server：尊重它（运维改服务器时就走这条）
		case strings.TrimSpace(cfg.EnrollToken) != "":
			payload, err := enrolltoken.Inspect(cfg.EnrollToken)
			if err == nil {
				cfg.Server = payload.Server
			} else if hasStored {
				fmt.Fprintf(os.Stderr,
					"neilico-agent: 警告：NEILICO_TOKEN 无法解析（%v）；已存在 %s，沿用其中保存的服务器 %s\n",
					err, cfg.StatePath, storedServer)
				cfg.Server = storedServer
			} else {
				return errors.New("cannot read server from enrollment token; pass --server")
			}
		case hasStored:
			cfg.Server = storedServer
		}
	}
	if strings.TrimSpace(cfg.Node.Name) == "" {
		cfg.Node.Name, _ = os.Hostname()
	}
	if strings.TrimSpace(cfg.Node.Name) == "" {
		cfg.Node.Name = "neilico-node"
	}
	return cfg.Validate()
}

// stateServer 读取 state.json 里保存的控制面地址。
//
//	已 enroll 过的节点，state 才是权威来源：有了它，令牌（一次性、会过期）就只是可选的。
func stateServer(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	stored, exists, err := state.Load(path)
	if err != nil || !exists || strings.TrimSpace(stored.Server) == "" {
		return "", false
	}
	return stored.Server, true
}

func newAPIClient(cfg config.Config) (*client.Client, error) {
	return client.NewWithTLS(cfg.Server, cfg.Token, client.TLSOptions{
		CAFile:             cfg.TLS.CAFile,
		ClientCertFile:     cfg.TLS.ClientCertFile,
		ClientKeyFile:      cfg.TLS.ClientKeyFile,
		ServerName:         cfg.TLS.ServerName,
		InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
	})
}

func ensureIdentity(ctx context.Context, cfg config.Config, apiClient *client.Client, force bool, logger *slog.Logger) (state.State, error) {
	return ensureIdentityWithCapabilities(ctx, cfg, apiClient, force, agentcapabilities.Detect(), logger)
}

func ensureIdentityWithCapabilities(ctx context.Context, cfg config.Config, apiClient *client.Client, force bool, reported capabilities.Capabilities, logger *slog.Logger) (state.State, error) {
	stored, exists, err := state.Load(cfg.StatePath)
	if err != nil {
		return state.State{}, err
	}
	if exists && !force {
		return stored, nil
	}
	if strings.TrimSpace(cfg.EnrollToken) != "" {
		return enrollAndSaveWithCapabilities(ctx, cfg, io.Discard, reported)
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return state.State{}, errors.New("node credentials are missing; run `neilico-agent enroll --token <TOKEN>`, or provide --token/--token-file/NEILICO_TOKEN")
	}
	var registered state.State
	err = heartbeat.RetryUntil(ctx, func(ctx context.Context) error {
		response, err := apiClient.Register(ctx, client.RegisterRequest{
			Name:         cfg.Node.Name,
			OS:           runtime.GOOS,
			Arch:         runtime.GOARCH,
			Version:      version,
			Tags:         cfg.Node.Tags,
			Capabilities: &reported,
		})
		if err != nil {
			return err
		}
		registered = state.State{
			NodeID:     response.NodeID,
			AgentToken: response.AgentToken,
			PrivateKey: response.PrivateKey,
			PublicKey:  response.PublicKey,
			Server:     cfg.Server,
		}
		return state.Save(cfg.StatePath, registered)
	}, nil, logger)
	if err != nil {
		return state.State{}, err
	}
	logger.Info("node registered", "node_id", registered.NodeID)
	return registered, nil
}

func enrollAndSave(ctx context.Context, cfg config.Config, stdout io.Writer) (state.State, error) {
	return enrollAndSaveWithCapabilities(ctx, cfg, stdout, agentcapabilities.Detect())
}

func enrollAndSaveWithCapabilities(ctx context.Context, cfg config.Config, stdout io.Writer, reported capabilities.Capabilities) (state.State, error) {
	stored, exists, err := state.Load(cfg.StatePath)
	if err != nil {
		return state.State{}, err
	}
	if exists {
		fmt.Fprintf(stdout, "node already enrolled: %s\n", stored.NodeID)
		return stored, nil
	}
	rawToken := strings.TrimSpace(cfg.EnrollToken)
	if rawToken == "" {
		return state.State{}, errors.New("enrollment token is required")
	}
	hostname, _ := os.Hostname()
	requestName := strings.TrimSpace(cfg.Node.Name)
	if requestName == "neilico-node" {
		requestName = ""
	}
	apiClient := client.New(cfg.Server, "")
	response, err := apiClient.Enroll(ctx, client.EnrollRequest{
		Token: rawToken, Name: requestName, Hostname: hostname,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Version: version, Tags: cfg.Node.Tags,
		Capabilities: &reported,
	})
	if err != nil {
		return state.State{}, client.SafeError(err, rawToken)
	}
	if response.Replayed && (response.AgentToken == "" || response.PrivateKey == "") {
		return state.State{}, fmt.Errorf("enrollment token was already used for node %s and its one-time credentials are unavailable; install the original state.json or issue a new token", response.NodeID)
	}
	identity := state.State{
		NodeID:     response.NodeID,
		AgentToken: response.AgentToken,
		PrivateKey: response.PrivateKey,
		PublicKey:  response.PublicKey,
		VirtualIP:  response.VirtualIP,
		NetworkID:  response.NetworkID,
		Server:     response.Server,
	}
	if identity.Server == "" {
		identity.Server = cfg.Server
	}
	if err := state.Save(cfg.StatePath, identity); err != nil {
		return state.State{}, err
	}
	fmt.Fprintf(stdout, "node enrolled: %s\n", identity.NodeID)
	if identity.VirtualIP != "" {
		fmt.Fprintf(stdout, "virtual ip: %s\n", identity.VirtualIP)
	}
	return identity, nil
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

// needsSchemaReapply 判断是否需要因为"本地应用逻辑版本"变化而重新应用配置。
// agent 升级（新增/修改本地动作，如为对端 AllowedIPs 加路由）后，服务端配置可能
// 一点没变；若此时按"版本已应用"短路返回，升级带来的新动作永远装不上（真机踩到）。
func needsSchemaReapply(appliedSchema int) bool {
	return appliedSchema != mesh.ApplicationSchemaVersion
}

func pollOnce(ctx context.Context, identity state.State, apiClient *client.Client, reconciler *mesh.Reconciler, observer *agentmetrics.Metrics, logger *slog.Logger) error {
	schemaChanged := needsSchemaReapply(identity.ApplicationSchema)
	result, err := apiClient.Config(ctx, identity.NodeID, identity.AppliedVersion)
	if err != nil {
		observer.ConfigPull("failure")
		logger.Warn("configuration pull failed", "error", err)
		return err
	}
	if schemaChanged {
		// 需要最新快照（NotModified 时响应里没有 delivery），拿到后交给 Reconcile
		latest, latestErr := apiClient.Config(ctx, identity.NodeID, 0)
		if latestErr != nil {
			observer.ConfigPull("failure")
			logger.Warn("configuration latest fetch failed", "error", latestErr)
			return latestErr
		}
		if !latest.NotModified && latest.Delivery.Version > 0 {
			result = latest
		} else if result.NotModified {
			observer.ConfigPull("not_modified")
			return nil
		}
		logger.Info("local application schema changed; re-applying configuration",
			"applied_schema", identity.ApplicationSchema, "agent_schema", mesh.ApplicationSchemaVersion)
	} else if result.NotModified {
		observer.ConfigPull("not_modified")
		return nil
	} else if result.Delivery.Version <= identity.AppliedVersion {
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
		// 同时上报本机内网地址：处于同一内网的设备之间可以直接用内网地址建隧道，
		// 不必依赖（常常不可达的）公网出口。注意即使公网地址探测失败也要上报——
		// 恰恰是这种环境（无公网出口）最需要内网直连。
		localAddresses := make([]string, 0, 4)
		for _, prefix := range mesh.LocalPrefixes(cfg.Mesh.Interface) {
			localAddresses = append(localAddresses, prefix.String())
		}
		if endpoint != "" || len(localAddresses) > 0 {
			current, exists, err := state.Load(cfg.StatePath)
			if err == nil && exists {
				identity = current
				due := time.Since(identity.LastEndpointReport) >= 5*time.Minute
				changed := identity.LastEndpoint != endpoint || !sameAddresses(identity.LastLocalAddresses, localAddresses)
				if changed || due {
					report := client.NetworkReportRequest{
						PublicEndpoint: endpoint,
						LocalAddresses: localAddresses,
						ListenPort:     cfg.Mesh.ListenPort,
					}
					if err := apiClient.ReportEndpoint(ctx, identity.NodeID, report); err != nil {
						logger.Warn("public endpoint report failed", "error", err)
					} else {
						identity.LastEndpoint = endpoint
						identity.LastLocalAddresses = localAddresses
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

// sameAddresses 比较两次上报的内网地址列表是否一致（顺序无关）。
func sameAddresses(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
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
	// WireGuard overlay addressing is 100.64.0.0/10 (CGNAT), matching NEILICO defaults.
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

func capabilitySnapshot() *capabilities.Capabilities {
	reported := agentcapabilities.Detect()
	return &reported
}

// shouldDryRun 决定是否只演练、不落盘网络配置。
//
// 除了显式的 --dry-run，只要任一承载 mesh 的能力未就绪就必须 dry-run：
// tunnel 由 Detect() 的真实建接口探测得出（见 agent/internal/capabilities），
// 它是应用 mesh 配置的门禁——tunnel 不可用即不应用，避免建不出 wg0 却反复重试，
// 也避免在能力自相矛盾时误判「就绪」而写入半截配置。
func shouldDryRun(requested bool, reported capabilities.Capabilities) bool {
	return requested || !reported.MeshApplicable()
}

func logCapabilities(reported capabilities.Capabilities, logger *slog.Logger) {
	logger.Info("agent capabilities detected", "mesh", reported.Mesh, "subnet_routes", reported.SubnetRoutes, "tunnel", reported.Tunnel)
	for _, item := range []struct {
		name   string
		status string
	}{
		{name: "mesh", status: reported.Mesh},
		{name: "subnet_routes", status: reported.SubnetRoutes},
		{name: "tunnel", status: reported.Tunnel},
	} {
		if item.status != "ready" {
			logger.Warn("能力不可用", "capability", item.name, "status", item.status, "reason", reported.Reason)
		}
	}
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
