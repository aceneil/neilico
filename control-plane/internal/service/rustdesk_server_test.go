package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRustDeskHelperProcess 是「被监管的假进程」。当 RD_HELPER=1 时，它按环境变量
// 监听指定端口后永久阻塞（等价于常驻服务），从而让监管器的启动/停止/端口探测可被真实验证。
// 非 helper 调用直接 skip，不影响正常测试。
func TestRustDeskHelperProcess(t *testing.T) {
	if os.Getenv("RD_HELPER") != "1" {
		t.Skip("not a helper invocation")
	}
	for _, raw := range strings.Split(os.Getenv("RD_HELPER_TCP"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		port, err := strconv.Atoi(raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad tcp port", raw)
			os.Exit(2)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			fmt.Fprintln(os.Stderr, "listen tcp", port, err)
			os.Exit(1)
		}
		defer listener.Close()
	}
	if raw := strings.TrimSpace(os.Getenv("RD_HELPER_UDP")); raw != "" {
		if port, err := strconv.Atoi(raw); err == nil {
			conn, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
			if err == nil {
				defer conn.Close()
			}
		}
	}
	select {}
}

// writeHelperScript 生成一个「以假进程身份运行当前测试二进制」的可执行脚本。
func writeHelperScript(t *testing.T, dir, name, tcpPorts, udpPort string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexport RD_HELPER=1\nexport RD_HELPER_TCP=%q\nexport RD_HELPER_UDP=%q\nexec %q -test.run=^TestRustDeskHelperProcess$\n",
		tcpPorts, udpPort, exe)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}
	return path
}

// freePorts 取 n 个当前空闲的 TCP 端口（避免与运行中的外部 hbbs/hbbr 冲突）。
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	ports := make([]int, 0, n)
	used := map[int]bool{}
	for len(ports) < n {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("allocate free port: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		if used[port] {
			continue
		}
		used[port] = true
		ports = append(ports, port)
	}
	return ports
}

type rdTestFixture struct {
	server    *RustDeskServer
	ctx       context.Context
	cancel    context.CancelFunc
	hbbsPorts []int // TCP（不含中继端口）
	relayPort int
	udpPort   int
}

func newRDFixture(t *testing.T, mode RustDeskServerMode, idle time.Duration, manual bool) *rdTestFixture {
	t.Helper()
	ports := freePorts(t, 5)
	all := append([]int(nil), ports...)
	relayPort := all[2]
	udpPort := all[1]
	hbbsTCP := []int{all[0], all[1], all[3], all[4]}
	dir := t.TempDir()

	hbbsTCPStr := joinInts(hbbsTCP)
	hbbrTCPStr := strconv.Itoa(relayPort)
	hbbsScript := writeHelperScript(t, dir, "hbbs", hbbsTCPStr, strconv.Itoa(udpPort))
	hbbrScript := writeHelperScript(t, dir, "hbbr", hbbrTCPStr, "")

	server := NewRustDeskServer(RustDeskServerOptions{
		Mode:        mode,
		IdleTimeout: idle,
		KeyDir:      dir,
		HBBSPath:    hbbsScript,
		HBBRPath:    hbbrScript,
		Ports:       all,
		RelayPort:   relayPort,
		UDPPort:     udpPort,
	})
	ctx, cancel := context.WithCancel(context.Background())
	return &rdTestFixture{
		server:    server,
		ctx:       ctx,
		cancel:    cancel,
		hbbsPorts: hbbsTCP,
		relayPort: relayPort,
		udpPort:   udpPort,
	}
}

func (f *rdTestFixture) close() {
	f.cancel()
	f.server.beginShutdown()
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func allEndpointsListening(status RustDeskServerStatus) bool {
	if len(status.Ports) != 6 {
		return false
	}
	for _, endpoint := range status.Ports {
		if !endpoint.Listening {
			return false
		}
	}
	return true
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return condition()
}

// 按需模式：初始无进程、无监听；触发后拉起并监听 6 个端点。
func TestRustDeskServerOnDemandStartsOnTrigger(t *testing.T) {
	fixture := newRDFixture(t, RustDeskModeOnDemand, time.Minute, false)
	defer fixture.close()

	// 初始：未运行、无监听、进程数为 0。
	initial := fixture.server.Status()
	if initial.Running {
		t.Fatalf("expected stopped initially, got running: %+v", initial)
	}
	if len(initial.ListeningPorts) != 0 {
		t.Fatalf("expected no listening ports initially, got %v", initial.ListeningPorts)
	}
	if fixture.server.hbbsPID != 0 || fixture.server.hbbrPID != 0 {
		t.Fatalf("expected zero child processes, got hbbs=%d hbbr=%d", fixture.server.hbbsPID, fixture.server.hbbrPID)
	}

	// 触发 -> 自动拉起。
	fixture.server.Trigger(context.Background())
	if !waitFor(t, 8*time.Second, func() bool { return allEndpointsListening(fixture.server.Status()) }) {
		t.Fatalf("expected all 6 endpoints listening after trigger, got %+v", fixture.server.Status())
	}

	status := fixture.server.Status()
	if !status.Running {
		t.Fatalf("expected running after trigger")
	}
	if len(status.ListeningPorts) != 5 {
		t.Fatalf("expected 5 unique listening ports (21116 兼 UDP+TCP), got %v", status.ListeningPorts)
	}
	if status.IdleRemainingSeconds == nil {
		t.Fatalf("expected idle countdown while running")
	}
	// 监听端口确实是配置的端口。
	for _, port := range append(append([]int(nil), fixture.hbbsPorts...), fixture.relayPort) {
		found := false
		for _, listening := range status.ListeningPorts {
			if listening == port {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected port %d listening, got %v", port, status.ListeningPorts)
		}
	}
}

// 空闲超时：无活动超过阈值后自动停止，且不再监听。
func TestRustDeskServerIdleTimeoutStops(t *testing.T) {
	fixture := newRDFixture(t, RustDeskModeOnDemand, time.Second, false)
	defer fixture.close()

	go fixture.server.Run(fixture.ctx)

	fixture.server.Trigger(context.Background())
	if !waitFor(t, 8*time.Second, func() bool { return allEndpointsListening(fixture.server.Status()) }) {
		t.Fatalf("expected endpoints listening after trigger, got %+v", fixture.server.Status())
	}

	// idle=1s，watchdog 每秒一拍；等待其自动回收。
	if !waitFor(t, 6*time.Second, func() bool { return !fixture.server.Status().Running }) {
		t.Fatalf("expected server to stop after idle timeout, got %+v", fixture.server.Status())
	}
	after := fixture.server.Status()
	if len(after.ListeningPorts) != 0 {
		t.Fatalf("expected no listening ports after idle stop, got %v", after.ListeningPorts)
	}
	if fixture.server.hbbsPID != 0 || fixture.server.hbbrPID != 0 {
		t.Fatalf("expected zero child processes after idle stop")
	}
}

// off 模式：永不启动；触发为 no-op，Start 返回 disabled。
func TestRustDeskServerOffModeNeverStarts(t *testing.T) {
	fixture := newRDFixture(t, RustDeskModeOff, time.Minute, false)
	defer fixture.close()

	go fixture.server.Run(fixture.ctx)

	fixture.server.Trigger(context.Background())
	if err := fixture.server.Start(context.Background(), true); err != ErrRustDeskServerDisabled {
		t.Fatalf("expected ErrRustDeskServerDisabled, got %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	status := fixture.server.Status()
	if status.Running {
		t.Fatalf("off mode must never start; got running")
	}
	if len(status.ListeningPorts) != 0 {
		t.Fatalf("off mode must not listen; got %v", status.ListeningPorts)
	}
}

// 幂等：重复 start 不产生重复进程；重复 stop 安全。
func TestRustDeskServerStartIsIdempotent(t *testing.T) {
	fixture := newRDFixture(t, RustDeskModeOnDemand, time.Minute, false)
	defer fixture.close()

	if err := fixture.server.Start(context.Background(), false); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if !waitFor(t, 8*time.Second, func() bool { return allEndpointsListening(fixture.server.Status()) }) {
		t.Fatalf("expected endpoints listening after first start")
	}
	firstPID := fixture.server.hbbsPID

	if err := fixture.server.Start(context.Background(), false); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if fixture.server.hbbsPID != firstPID {
		t.Fatalf("second start must be idempotent: pid changed %d -> %d", firstPID, fixture.server.hbbsPID)
	}
	if err := fixture.server.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := fixture.server.Stop(context.Background()); err != nil {
		t.Fatalf("second stop must be safe: %v", err)
	}
	if fixture.server.Status().Running {
		t.Fatalf("expected stopped after Stop")
	}
}

// 手动保持：admin 手动启动后，空闲 watchdog 不自动停；手动停止后回落。
func TestRustDeskServerManualPinSurvivesIdle(t *testing.T) {
	fixture := newRDFixture(t, RustDeskModeOnDemand, time.Second, true)
	defer fixture.close()

	go fixture.server.Run(fixture.ctx)

	if err := fixture.server.Start(context.Background(), true); err != nil {
		t.Fatalf("manual start: %v", err)
	}
	if !waitFor(t, 8*time.Second, func() bool { return allEndpointsListening(fixture.server.Status()) }) {
		t.Fatalf("expected endpoints listening after manual start")
	}

	// 超过 idle 阈值后仍应运行（manual 抑制自动回收）。
	time.Sleep(2500 * time.Millisecond)
	if !fixture.server.Status().Running {
		t.Fatalf("manual-pinned server must survive idle timeout")
	}

	if err := fixture.server.Stop(context.Background()); err != nil {
		t.Fatalf("manual stop: %v", err)
	}
	if !waitFor(t, 3*time.Second, func() bool { return !fixture.server.Status().Running }) {
		t.Fatalf("expected stopped after manual stop")
	}
}
