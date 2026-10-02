package mesh

import (
	"bytes"
	"context"
	"errors"
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
