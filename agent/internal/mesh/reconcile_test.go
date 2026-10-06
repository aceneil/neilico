package mesh

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"neilico/agent/internal/client"
	"neilico/agent/internal/route"
	"neilico/agent/internal/state"
)

type countingApplier struct {
	calls  int
	config Config
	fail   bool
}

func (a *countingApplier) Apply(_ context.Context, config Config) error {
	a.calls++
	a.config = config
	if a.fail {
		return errors.New("apply failed")
	}
	return nil
}
func (a *countingApplier) Cleanup(context.Context) error { return nil }

type countingRoutes struct {
	applyCalls int
	planCalls  int
}

func (r *countingRoutes) Apply(context.Context, []route.Route) ([]route.Command, error) {
	r.applyCalls++
	return nil, nil
}
func (r *countingRoutes) Plan([]route.Route) ([]route.Command, error) {
	r.planCalls++
	return []route.Command{{Name: "ip", Args: []string{"route", "add", "192.168.1.0/24", "dev", "wg0"}}}, nil
}
func (r *countingRoutes) Cleanup(context.Context) error { return nil }

func TestConfigHashIgnoresVersionButDetectsContent(t *testing.T) {
	delivery := client.Delivery{Version: 1, WireGuardConfig: "[Interface]\nPrivateKey = x\n"}
	first, err := ConfigHash(delivery)
	if err != nil {
		t.Fatal(err)
	}
	delivery.Version = 2
	second, err := ConfigHash(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("hash changed when only version changed")
	}
	delivery.WireGuardConfig += "\n[Peer]\n"
	third, err := ConfigHash(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("hash did not change with configuration")
	}
}

func TestReconcileUnchangedOnlyUpdatesVersion(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	seedState(t, statePath)
	applier := &countingApplier{}
	routes := &countingRoutes{}
	reconciler := NewReconciler(ReconcilerOptions{StatePath: statePath, Applier: applier, Routes: routes, Interface: "wg0", MTU: 1420, ListenPort: 51820})
	delivery := client.Delivery{Version: 1, Node: client.NodeIdentity{ID: "node"}, WireGuardConfig: "[Interface]\nPrivateKey = x\n"}
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 || routes.applyCalls != 1 {
		t.Fatalf("first apply calls = %d/%d", applier.calls, routes.applyCalls)
	}
	delivery.Version = 2
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 || routes.applyCalls != 1 {
		t.Fatalf("unchanged apply calls = %d/%d, want 1/1", applier.calls, routes.applyCalls)
	}
}

func TestDryRunPrintsFullConfigAndDoesNotExecute(t *testing.T) {
	executor := &recordingExecutor{}
	shell := NewShellApplier(executor, t.TempDir())
	var output bytes.Buffer
	applier := NewDryRunApplier(shell, &output)
	delivery := client.Delivery{
		Node:            client.NodeIdentity{ID: "node"},
		WireGuardConfig: "[Interface]\nPrivateKey = LOCAL_PRIVATE_KEY\nAddress = 100.64.0.2/32\n\n[Peer]\nPublicKey = PEER_PUBLIC\nAllowedIPs = 100.64.0.3/32\n",
		Network:         &client.Network{Peers: []client.Peer{{PublicKey: "PEER_PUBLIC", AllowedIPs: []string{"100.64.0.3/32"}}}},
	}
	routes := &countingRoutes{}
	reconciler := NewReconciler(ReconcilerOptions{
		StatePath:  seededStatePath(t),
		Applier:    applier,
		Routes:     routes,
		Output:     &output,
		DryRun:     true,
		Interface:  "wg0",
		MTU:        1420,
		ListenPort: 51820,
	})
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("dry-run executed commands: %#v", executor.commands)
	}
	if routes.applyCalls != 0 || routes.planCalls != 1 {
		t.Fatalf("route calls = apply %d plan %d", routes.applyCalls, routes.planCalls)
	}
	text := output.String()
	for _, expected := range []string{"LOCAL_PRIVATE_KEY", "PublicKey = PEER_PUBLIC", "AllowedIPs = 100.64.0.3/32", "peer PEER_PUBLIC allowed_ips=100.64.0.3/32"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("dry-run output omitted %q:\n%s", expected, text)
		}
	}
}

func seedState(t *testing.T, path string) {
	t.Helper()
	if err := state.Save(path, state.State{NodeID: "node", AgentToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
}

func seededStatePath(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/state.json"
	seedState(t, path)
	return path
}

// fakeProbe 是可注入的漂移探测器假实现，避免单测依赖 root 权限或真实内核网络。
type fakeProbe struct {
	missing []string
	err     error
	calls   int
	last    Config
}

func (p *fakeProbe) Missing(_ context.Context, config Config) ([]string, error) {
	p.calls++
	p.last = config
	return p.missing, p.err
}

// 状态校对的核心回归：配置未变、但本地实物已漂移（容器重启清空 netns → wg0 消失）时，
// 必须无条件强制重新应用，绝不能因版本号/哈希未变而短路。同时保留"实物完好即跳过"的优化。
func TestReconcileReappliesOnLocalDriftDespiteUnchangedHash(t *testing.T) {
	statePath := seededStatePath(t)
	applier := &countingApplier{}
	routes := &countingRoutes{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	probe := &fakeProbe{}
	reconciler := NewReconciler(ReconcilerOptions{
		StatePath:  statePath,
		Applier:    applier,
		Routes:     routes,
		Probe:      probe,
		Logger:     logger,
		Interface:  "wg0",
		MTU:        1420,
		ListenPort: 51820,
	})
	delivery := client.Delivery{Version: 1, Node: client.NodeIdentity{ID: "node"}, WireGuardConfig: "[Interface]\nPrivateKey = x\n"}
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 {
		t.Fatalf("首次应用调用次数 = %d，期望 1", applier.calls)
	}

	// ② 配置未变、本地实物完好（探测器报告无缺失）→ 不得重建。
	delivery.Version = 2
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 {
		t.Fatalf("实物完好时不应重新应用：调用次数 = %d", applier.calls)
	}

	// ① 配置未变，但本地实物缺失（如容器重启后 wg0 消失）→ 必须强制重新应用。
	probe.missing = []string{"interface wg0 missing"}
	delivery.Version = 3
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 2 {
		t.Fatalf("检测到漂移时必须重新应用：调用次数 = %d，期望 2", applier.calls)
	}
	if !strings.Contains(logs.String(), "local mesh state drift detected; re-applying") {
		t.Fatalf("漂移重应用必须打 INFO 日志，实际日志：\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "interface wg0 missing") {
		t.Fatalf("漂移日志必须带上缺失项，实际日志：\n%s", logs.String())
	}

	// ③ 配置发生变化 → 仍然重新应用（与漂移无关的常规路径）。
	probe.missing = nil
	delivery.WireGuardConfig += "\n[Peer]\nPublicKey = y\n"
	delivery.Version = 4
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 3 {
		t.Fatalf("配置变化时必须重新应用：调用次数 = %d，期望 3", applier.calls)
	}
}

// 探测本身报错（工具缺失等）不应被当成漂移，避免误判引发反复重建。
func TestReconcileProbeErrorDoesNotForceReapply(t *testing.T) {
	statePath := seededStatePath(t)
	applier := &countingApplier{}
	probe := &fakeProbe{err: errors.New("wg tool missing")}
	reconciler := NewReconciler(ReconcilerOptions{StatePath: statePath, Applier: applier, Routes: &countingRoutes{}, Probe: probe, Interface: "wg0"})
	delivery := client.Delivery{Version: 1, Node: client.NodeIdentity{ID: "node"}, WireGuardConfig: "[Interface]\nPrivateKey = x\n"}
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	delivery.Version = 2
	if err := reconciler.Reconcile(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if applier.calls != 1 {
		t.Fatalf("探测失败时不应强制重建：调用次数 = %d", applier.calls)
	}
}

// LocalDrift：只有 state 声称"已应用"时才校对；接口缺失即报告漂移。
func TestLocalDriftReportsMissingInterfaceOnlyWhenClaimedApplied(t *testing.T) {
	statePath := seededStatePath(t)
	probe := &fakeProbe{}
	reconciler := NewReconciler(ReconcilerOptions{StatePath: statePath, Probe: probe, Interface: "wg0"})

	// 从未应用过：不检测。
	missing, err := reconciler.LocalDrift(context.Background())
	if err != nil || len(missing) != 0 {
		t.Fatalf("未声称应用时不应报告漂移：missing=%v err=%v", missing, err)
	}
	if probe.calls != 0 {
		t.Fatalf("未声称应用时不应调用探测器：calls=%d", probe.calls)
	}

	// state 声称已应用 + 本地接口缺失 → 报告漂移。
	stored, _, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	stored.AppliedVersion = 1155
	stored.ApplicationSchema = ApplicationSchemaVersion
	stored.AppliedConfigHash = "deadbeef"
	stored.AppliedPeers = []state.AppliedPeer{{PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", AllowedIPs: []string{"100.64.0.3/32"}}}
	if err := state.Save(statePath, stored); err != nil {
		t.Fatal(err)
	}
	probe.missing = []string{"interface wg0 missing"}
	missing, err = reconciler.LocalDrift(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) == 0 {
		t.Fatal("state 声称已应用但接口缺失时必须报告漂移")
	}
	if len(probe.last.Peers) != 1 || probe.last.Peers[0].PublicKey != stored.AppliedPeers[0].PublicKey {
		t.Fatalf("校对必须带上落盘的期望对端：%#v", probe.last.Peers)
	}
}

type recordingExecutor struct {
	commands []route.Command
	outputs  map[string]string
}

func (e *recordingExecutor) Run(_ context.Context, command route.Command) error {
	e.commands = append(e.commands, command)
	return nil
}
func (e *recordingExecutor) Output(_ context.Context, command route.Command) (string, error) {
	if e.outputs == nil {
		return "", nil
	}
	return e.outputs[command.String()], nil
}
