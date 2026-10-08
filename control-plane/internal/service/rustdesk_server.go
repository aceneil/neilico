package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RustDeskServerMode 决定 hbbs/hbbr 的生命周期策略。
//
//   - on_demand（默认）：无远程桌面活动时不启动（不监听 21115-21119、进程数 0）；
//     收到相关请求时自动拉起，空闲超过阈值后自动停止。
//   - always_on：控制面启动即拉起，常驻（仍可被 admin 手动停）。
//   - off：永不启动；任何触发都视为不可用。
type RustDeskServerMode string

const (
	RustDeskModeOnDemand RustDeskServerMode = "on_demand"
	RustDeskModeAlwaysOn RustDeskServerMode = "always_on"
	RustDeskModeOff      RustDeskServerMode = "off"
)

// 默认值与固定端口归属。hbbr 独占 21117（中继），21116 兼有 UDP。
const (
	DefaultRustDeskIdleTimeout = 10 * time.Minute
	DefaultRustDeskHBBSPath    = "/usr/local/bin/hbbs"
	DefaultRustDeskHBBRPath    = "/usr/local/bin/hbbr"
	DefaultRustDeskRelayPort   = 21117
	DefaultRustDeskUDPPort     = 21116
	rustDeskStartTimeout       = 10 * time.Second
	rustDeskStopTimeout        = 8 * time.Second
)

// ErrRustDeskServerDisabled 在 mode=off 时返回（接口层译为 409）。
var ErrRustDeskServerDisabled = errors.New("remote desktop server is disabled (mode=off)")

// ParseRustDeskServerMode 解析字符串，非法值回落到默认 on_demand。
func ParseRustDeskServerMode(value string) RustDeskServerMode {
	switch RustDeskServerMode(strings.ToLower(strings.TrimSpace(value))) {
	case RustDeskModeOnDemand, "on-demand", "ondemand", "demand":
		return RustDeskModeOnDemand
	case RustDeskModeAlwaysOn, "always", "on", "alwayson":
		return RustDeskModeAlwaysOn
	case RustDeskModeOff, "disabled", "none", "no":
		return RustDeskModeOff
	default:
		return ""
	}
}

// RustDeskServerOptions 是进程监管模块的启动配置（来自 config.RemoteDesktop）。
type RustDeskServerOptions struct {
	Mode        RustDeskServerMode
	IdleTimeout time.Duration
	KeyDir      string
	HBBSPath    string
	HBBRPath    string
	// RelayHost 非空时以 `-r <relay>` 下发给 hbbs（显式告诉客户端中继地址）；留空则不带该参数。
	RelayHost string
	// Ports 是 hbbs/hbbr 监听的端口集合（默认 21115..21119）。
	Ports []int
	// RelayPort 归 hbbr（默认 21117），其余端口归 hbbs。
	RelayPort int
	// UDPPort 额外以 UDP 监听的端口（默认 21116）。
	UDPPort int
	Logger  *slog.Logger
}

// RustDeskEndpoint 是单个监听端点的实时状态。
type RustDeskEndpoint struct {
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Owner     string `json:"owner"`
	Listening bool   `json:"listening"`
}

// RustDeskServerStatus 是 `/remote-desktop/server-status` 的响应体。
//
// 安全约定：**只含公钥路径与「公钥是否就绪」布尔值，绝不含私钥内容或任何令牌。**
type RustDeskServerStatus struct {
	Mode                 string             `json:"mode"`
	Running              bool               `json:"running"`
	Manual               bool               `json:"manual"`
	Ports                []RustDeskEndpoint `json:"ports"`
	ListeningPorts       []int              `json:"listening_ports"`
	LastActivity         *time.Time         `json:"last_activity"`
	IdleTimeoutSeconds   int                `json:"idle_timeout_seconds"`
	IdleRemainingSeconds *int               `json:"idle_remaining_seconds"`
	StartedAt            *time.Time         `json:"started_at,omitempty"`
	KeyDir               string             `json:"key_dir"`
	PublicKeyPath        string             `json:"public_key_path"`
	PublicKeyReady       bool               `json:"public_key_ready"`
	HBBSLog              string             `json:"hbbs_log,omitempty"`
	HBBRLog              string             `json:"hbbr_log,omitempty"`
	LastError            string             `json:"last_error,omitempty"`
}

// RustDeskServer 是 hbbs/hbbr 的进程监管器：按需拉起、空闲回收、手动开关。
//
// 并发约定：所有可变状态由 mu 保护；startLocked/stopLocked 必须在持有 mu 时调用。
type RustDeskServer struct {
	mu           sync.Mutex
	opts         RustDeskServerOptions
	mode         RustDeskServerMode
	idleTimeout  time.Duration
	keyDir       string
	hbbsPath     string
	hbbrPath     string
	relayHost    string
	ports        []int
	relayPort    int
	udpPort      int
	endpoints    []RustDeskEndpoint
	logger       *slog.Logger
	hbbsCmd      *exec.Cmd
	hbbrCmd      *exec.Cmd
	hbbsPID      int
	hbbrPID      int
	running      bool
	manual       bool
	startedAt    time.Time
	lastActivity time.Time
	lastError    string
}

// NewRustDeskServer 构造监管器（不启动任何进程）。
func NewRustDeskServer(opts RustDeskServerOptions) *RustDeskServer {
	mode := opts.Mode
	if mode == "" {
		mode = RustDeskModeOnDemand
	}
	idle := opts.IdleTimeout
	if idle <= 0 {
		idle = DefaultRustDeskIdleTimeout
	}
	keyDir := strings.TrimSpace(opts.KeyDir)
	if keyDir == "" {
		keyDir = "/var/lib/neilico/rustdesk"
	}
	hbbs := strings.TrimSpace(opts.HBBSPath)
	if hbbs == "" {
		hbbs = DefaultRustDeskHBBSPath
	}
	hbbr := strings.TrimSpace(opts.HBBRPath)
	if hbbr == "" {
		hbbr = DefaultRustDeskHBBRPath
	}
	relayPort := opts.RelayPort
	if relayPort <= 0 {
		relayPort = DefaultRustDeskRelayPort
	}
	udpPort := opts.UDPPort
	if udpPort < 0 {
		udpPort = 0
	} else if udpPort == 0 {
		udpPort = DefaultRustDeskUDPPort
	}
	ports := normalizeRemoteDesktopPorts(opts.Ports)
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	server := &RustDeskServer{
		opts:        opts,
		mode:        mode,
		idleTimeout: idle,
		keyDir:      keyDir,
		hbbsPath:    hbbs,
		hbbrPath:    hbbr,
		relayHost:   strings.TrimSpace(opts.RelayHost),
		ports:       ports,
		relayPort:   relayPort,
		udpPort:     udpPort,
		logger:      logger,
	}
	server.endpoints = buildEndpoints(ports, relayPort, udpPort)
	return server
}

func buildEndpoints(ports []int, relayPort, udpPort int) []RustDeskEndpoint {
	result := make([]RustDeskEndpoint, 0, len(ports)+1)
	for _, port := range ports {
		owner := ownerForPort(port, relayPort)
		result = append(result, RustDeskEndpoint{Port: port, Protocol: "tcp", Owner: owner})
	}
	if udpPort > 0 {
		result = append(result, RustDeskEndpoint{Port: udpPort, Protocol: "udp", Owner: ownerForPort(udpPort, relayPort)})
	}
	return result
}

func ownerForPort(port, relayPort int) string {
	if port == relayPort {
		return "hbbr"
	}
	return "hbbs"
}

// Mode 返回当前生效的模式。
func (s *RustDeskServer) Mode() RustDeskServerMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// KeyDir 返回密钥目录（供配置层派生公钥路径）。
func (s *RustDeskServer) KeyDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keyDir
}

// PublicKeyPath 返回公钥文件路径（id_ed25519.pub）——只暴露公钥，绝不暴露私钥。
func (s *RustDeskServer) PublicKeyPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Join(s.keyDir, "id_ed25519.pub")
}

func (s *RustDeskServer) hbbsLogPath() string { return filepath.Join(s.keyDir, "hbbs.log") }
func (s *RustDeskServer) hbbrLogPath() string { return filepath.Join(s.keyDir, "hbbr.log") }

// Trigger 是「有远程桌面活动」的入口：刷新活跃时间，并在需要时按需拉起进程。
// mode=off 时静默 no-op（不启动、不报错）。
func (s *RustDeskServer) Trigger(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == RustDeskModeOff {
		return
	}
	s.lastActivity = time.Now().UTC()
	if s.running {
		return
	}
	if err := s.startLocked(ctx, false); err != nil {
		s.logger.Warn("rustdesk server on-demand start failed", "error", err)
	}
}

// Start 由接口层调用（admin）。manual=true 时进入「手动保持」——空闲 watchdog 不再自动停，
// 直到显式 Stop。重复调用幂等。
func (s *RustDeskServer) Start(ctx context.Context, manual bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == RustDeskModeOff {
		return ErrRustDeskServerDisabled
	}
	if s.running {
		s.lastActivity = time.Now().UTC()
		if manual {
			s.manual = true
		}
		return nil
	}
	return s.startLocked(ctx, manual)
}

// Stop 由接口层调用（admin）：停止进程并清除手动保持标志。重复调用幂等。
func (s *RustDeskServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manual = false
	if !s.running {
		return nil
	}
	s.stopLocked()
	return nil
}

// beginShutdown 是进程退出时的清理入口。
func (s *RustDeskServer) beginShutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.stopLocked()
	}
}

// Run 启动空闲回收 watchdog，阻塞直到 ctx 结束；返回时确保子进程已收尾。
func (s *RustDeskServer) Run(ctx context.Context) {
	if s.mode == RustDeskModeAlwaysOn {
		startCtx, cancel := context.WithTimeout(ctx, rustDeskStartTimeout)
		_ = s.Start(startCtx, false)
		cancel()
	}
	interval := s.idleTimeout / 10
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.beginShutdown()
			return
		case <-ticker.C:
			s.reapIfIdle()
		}
	}
}

func (s *RustDeskServer) reapIfIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	// 子进程意外死亡：清理状态，等待下次触发重新拉起。
	if !s.processesAlive() {
		s.logger.Warn("rustdesk server process exited unexpectedly; marking stopped")
		s.cleanupLocked()
		return
	}
	if s.manual || s.idleTimeout <= 0 {
		return
	}
	if time.Since(s.lastActivity) >= s.idleTimeout {
		s.logger.Info("rustdesk server idle timeout reached; stopping", "idle_seconds", int(time.Since(s.lastActivity).Seconds()))
		s.stopLocked()
	}
}

// startLocked 必须在持有 mu 时调用。
func (s *RustDeskServer) startLocked(ctx context.Context, manual bool) error {
	if err := s.ensureKeyDir(); err != nil {
		s.lastError = err.Error()
		return err
	}
	// 先启动 hbbs（首次运行会生成密钥对），再启动 hbbr（-k _ 需读取同一把 id_ed25519）。
	if err := s.spawnLocked("hbbs", s.hbbsPath, s.hbbsArgs()); err != nil {
		s.lastError = err.Error()
		s.cleanupLocked()
		return err
	}
	s.waitForKey(ctx, time.Now().Add(rustDeskStartTimeout))
	if err := s.spawnLocked("hbbr", s.hbbrPath, s.hbbrArgs()); err != nil {
		s.lastError = err.Error()
		s.cleanupLocked()
		return err
	}
	s.running = true
	s.manual = manual
	now := time.Now().UTC()
	s.startedAt = now
	s.lastActivity = now
	s.lastError = ""

	if missing := s.waitForEndpoints(ctx); len(missing) > 0 {
		message := fmt.Sprintf("rustdesk server started but endpoints not listening: %v", missing)
		s.lastError = message
		s.logger.Warn("rustdesk server endpoints not ready", "missing", missing)
	}
	return nil
}

func (s *RustDeskServer) ensureKeyDir() error {
	if err := os.MkdirAll(s.keyDir, 0o700); err != nil {
		return fmt.Errorf("create rustdesk key dir: %w", err)
	}
	// 目录权限收紧到 700（密钥目录约定）。
	if err := os.Chmod(s.keyDir, 0o700); err != nil {
		return fmt.Errorf("chmod rustdesk key dir: %w", err)
	}
	return nil
}

func (s *RustDeskServer) hbbsArgs() []string {
	var args []string
	if s.relayHost != "" {
		args = append(args, "-r", s.relayHost)
	}
	return args
}

// hbbrArgs：`-k _` = 从工作目录 id_ed25519 取 Key 并强制校验（公网防滥用，本次强制的加固项）。
func (s *RustDeskServer) hbbrArgs() []string {
	return []string{"-k", "_"}
}

// spawnLocked 启动一个子进程（独立进程组，工作目录=密钥目录）。必须在持有 mu 时调用。
func (s *RustDeskServer) spawnLocked(name, path string, args []string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("rustdesk %s binary not found at %s: %w", name, path, err)
	}
	logFile, err := os.OpenFile(s.logPathFor(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s log: %w", name, err)
	}
	command := exec.Command(path, args...)
	command.Dir = s.keyDir
	command.Stdout = logFile
	command.Stderr = logFile
	// 独立进程组：停止时按整个组发信号，避免留下孤儿子进程。
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start %s: %w", name, err)
	}
	pid := command.Process.Pid
	if name == "hbbs" {
		s.hbbsCmd = command
		s.hbbsPID = pid
	} else {
		s.hbbrCmd = command
		s.hbbrPID = pid
	}
	s.logger.Info("rustdesk server process started", "process", name, "pid", pid)
	return nil
}

func (s *RustDeskServer) logPathFor(name string) string {
	if name == "hbbs" {
		return s.hbbsLogPath()
	}
	return s.hbbrLogPath()
}

// waitForKey 等待 hbbs 生成 id_ed25519（首次启动）或 hbbs 端口已监听，最多等到 deadline。
func (s *RustDeskServer) waitForKey(ctx context.Context, deadline time.Time) {
	keyPath := filepath.Join(s.keyDir, "id_ed25519")
	for {
		if _, err := os.Stat(keyPath); err == nil {
			return
		}
		if s.portListening(s.udpPort) {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// waitForEndpoints 轮询直到所有 TCP 端点监听或超时，返回仍缺失的端口。
func (s *RustDeskServer) waitForEndpoints(ctx context.Context) []int {
	deadline := time.Now().Add(rustDeskStartTimeout)
	for {
		missing := s.missingTCPEndpoints()
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return missing
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *RustDeskServer) missingTCPEndpoints() []int {
	var missing []int
	for _, endpoint := range s.endpoints {
		if endpoint.Protocol != "tcp" {
			continue
		}
		if !s.portListening(endpoint.Port) {
			missing = append(missing, endpoint.Port)
		}
	}
	return missing
}

// stopLocked 终止两个子进程。必须在持有 mu 时调用。
func (s *RustDeskServer) stopLocked() {
	s.terminateLocked(s.hbbsCmd, s.hbbsPID)
	s.terminateLocked(s.hbbrCmd, s.hbbrPID)
	s.cleanupLocked()
	s.logger.Info("rustdesk server stopped")
}

// terminateLocked 先按进程组发 SIGTERM 并回收（cmd.Wait 会 reap，避免僵尸进程被误判为存活），
// 超时才升级为 SIGKILL。必须在持有 mu 时调用。
func (s *RustDeskServer) terminateLocked(command *exec.Cmd, pid int) {
	if command == nil || pid <= 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(rustDeskStopTimeout):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func (s *RustDeskServer) cleanupLocked() {
	s.running = false
	s.manual = false
	s.hbbsCmd = nil
	s.hbbrCmd = nil
	s.hbbsPID = 0
	s.hbbrPID = 0
	s.startedAt = time.Time{}
}

func (s *RustDeskServer) processesAlive() bool {
	if s.hbbsPID <= 0 || s.hbbrPID <= 0 {
		return false
	}
	return processAlive(s.hbbsPID) && processAlive(s.hbbrPID)
}

// Status 返回实时状态（供接口层）。
func (s *RustDeskServer) Status() RustDeskServerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	running := s.running && s.processesAlive()
	status := RustDeskServerStatus{
		Mode:           string(s.mode),
		Running:        running,
		Manual:         s.manual,
		KeyDir:         s.keyDir,
		PublicKeyPath:  filepath.Join(s.keyDir, "id_ed25519.pub"),
		PublicKeyReady: publicKeyReady(filepath.Join(s.keyDir, "id_ed25519.pub")),
		LastError:      s.lastError,
	}
	status.IdleTimeoutSeconds = int(s.idleTimeout.Seconds())
	seenPort := map[int]bool{}
	for _, endpoint := range s.endpoints {
		endpoint.Listening = running && s.endpointListening(endpoint)
		status.Ports = append(status.Ports, endpoint)
		if endpoint.Listening && !seenPort[endpoint.Port] {
			seenPort[endpoint.Port] = true
			status.ListeningPorts = append(status.ListeningPorts, endpoint.Port)
		}
	}
	if running {
		status.HBBSLog = s.hbbsLogPath()
		status.HBBRLog = s.hbbrLogPath()
	}
	if !s.lastActivity.IsZero() {
		activity := s.lastActivity
		status.LastActivity = &activity
	}
	if running {
		startedAt := s.startedAt
		status.StartedAt = &startedAt
		if s.idleTimeout > 0 {
			remaining := int((s.idleTimeout - time.Since(s.lastActivity)).Seconds())
			if remaining < 0 {
				remaining = 0
			}
			status.IdleRemainingSeconds = &remaining
		}
	}
	return status
}

func publicKeyReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func (s *RustDeskServer) endpointListening(endpoint RustDeskEndpoint) bool {
	if endpoint.Protocol == "udp" {
		return udpPortListening(endpoint.Port)
	}
	return s.portListening(endpoint.Port)
}

// portListening 判定本机某 TCP 端口是否监听（短超时拨号）。
func (s *RustDeskServer) portListening(port int) bool {
	if port <= 0 {
		return false
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

// udpPortListening 通过 /proc/net/udp[6] 判定 UDP 端口是否绑定（无需外部工具）。
func udpPortListening(port int) bool {
	if port <= 0 {
		return false
	}
	hexPort := strings.ToUpper(fmt.Sprintf("%04X", port))
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			// 第 2 列形如 "00000000:0140"，冒号后为本地端口（十六进制）。
			local := fields[1]
			if index := strings.LastIndex(local, ":"); index >= 0 {
				if strings.EqualFold(local[index+1:], hexPort) {
					return true
				}
			}
		}
	}
	return false
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err == nil {
		return true
	}
	return false
}
