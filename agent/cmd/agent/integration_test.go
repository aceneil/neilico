package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"neilico/control-plane/testkit"

	"neilico/agent/internal/client"
	"neilico/agent/internal/config"
	"neilico/agent/internal/heartbeat"
	"neilico/agent/internal/mesh"
	agentmetrics "neilico/agent/internal/metrics"
	"neilico/agent/internal/route"
	"neilico/agent/internal/state"
)

type integrationApp struct {
	control *testkit.App
	token   string
}

func newIntegrationApp(t *testing.T) *integrationApp {
	t.Helper()
	control := testkit.New(t)
	var response struct {
		Token string `json:"token"`
	}
	request(t, control.Server.URL, "POST", "/api/v1/auth/login", "", map[string]string{
		"email":    testkit.AdminEmail,
		"password": testkit.AdminPassword,
	}, &response)
	return &integrationApp{control: control, token: response.Token}
}

func (a *integrationApp) node(t *testing.T, name string) client.RegisterResponse {
	t.Helper()
	api := client.New(a.control.Server.URL, a.token)
	response, err := api.Register(context.Background(), client.RegisterRequest{
		Name:    name,
		OS:      "linux",
		Arch:    "amd64",
		Version: "m3-test",
		Tags:    []string{"integration"},
	})
	if err != nil {
		t.Fatalf("register integration node: %v", err)
	}
	return response
}

func (a *integrationApp) createAndJoin(t *testing.T, name string, nodes ...client.RegisterResponse) string {
	t.Helper()
	var network struct {
		ID string `json:"id"`
	}
	request(t, a.control.Server.URL, "POST", "/api/v1/networks", a.token, map[string]string{"name": name, "cidr": "100.64.0.0/24"}, &network)
	for _, node := range nodes {
		request(t, a.control.Server.URL, "POST", "/api/v1/networks/"+network.ID+"/members", a.token, map[string]string{"node_id": node.NodeID}, nil)
	}
	return network.ID
}

func request(t *testing.T, base, method, path, token string, input, output any) {
	t.Helper()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("request %s %s returned status %d", method, path, response.StatusCode)
	}
	if output != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			t.Fatalf("decode response for %s %s: %v", method, path, err)
		}
	}
}

func integrationConfig(t *testing.T, app *integrationApp, statePath string) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Server = app.control.Server.URL
	cfg.Token = app.token
	cfg.StatePath = statePath
	cfg.Metrics.Enabled = false
	cfg.PollInterval = config.Duration(time.Second)
	cfg.HeartbeatInterval = config.Duration(time.Second)
	return cfg
}

func TestRegistrationHeartbeatConfigAndState(t *testing.T) {
	app := newIntegrationApp(t)
	statePath := filepath.Join(t.TempDir(), "state.json")
	cfg := integrationConfig(t, app, statePath)
	api := client.New(cfg.Server, cfg.Token)
	identity, err := ensureIdentity(context.Background(), cfg, api, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("ensureIdentity() error = %v", err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
	api.SetToken(identity.AgentToken)
	if _, err := api.Heartbeat(context.Background(), identity.NodeID, "m3-test"); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	app.createAndJoin(t, "m3-network", identityNode(t, identity))
	result, err := api.Config(context.Background(), identity.NodeID, identity.AppliedVersion)
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if result.NotModified || result.Delivery.Network == nil || result.Delivery.WireGuardConfig == "" {
		t.Fatalf("configuration result did not contain active network and WireGuard config")
	}
}

func identityNode(t *testing.T, identity state.State) client.RegisterResponse {
	t.Helper()
	return client.RegisterResponse{NodeID: identity.NodeID, AgentToken: identity.AgentToken, PrivateKey: identity.PrivateKey, PublicKey: identity.PublicKey}
}

type integrationApplier struct {
	calls atomic.Int64
	last  mesh.Config
	inner mesh.Applier
}

func (a *integrationApplier) Apply(ctx context.Context, config mesh.Config) error {
	a.calls.Add(1)
	a.last = config
	if a.inner != nil {
		return a.inner.Apply(ctx, config)
	}
	return nil
}
func (a *integrationApplier) Cleanup(ctx context.Context) error {
	if a.inner != nil {
		return a.inner.Cleanup(ctx)
	}
	return nil
}

type integrationRoutes struct {
	applyCalls atomic.Int64
}

func (r *integrationRoutes) Apply(context.Context, []route.Route) ([]route.Command, error) {
	r.applyCalls.Add(1)
	return nil, nil
}
func (r *integrationRoutes) Plan([]route.Route) ([]route.Command, error) {
	return nil, nil
}
func (r *integrationRoutes) Cleanup(context.Context) error { return nil }

func Test304SkipsApplierAndVersionUpdateAppliesOnce(t *testing.T) {
	app := newIntegrationApp(t)
	nodeA := app.node(t, "node-a")
	nodeB := app.node(t, "node-b")
	networkID := app.createAndJoin(t, "versioned-network", nodeA, nodeB)
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := state.Save(statePath, state.State{NodeID: nodeA.NodeID, AgentToken: nodeA.AgentToken, PrivateKey: nodeA.PrivateKey, PublicKey: nodeA.PublicKey}); err != nil {
		t.Fatal(err)
	}
	api := client.New(app.control.Server.URL, nodeA.AgentToken)
	var dryOutput bytes.Buffer
	shell := mesh.NewShellApplier(&meshRecordingExecutor{}, t.TempDir())
	applier := &integrationApplier{inner: mesh.NewDryRunApplier(shell, &dryOutput)}
	routes := &integrationRoutes{}
	reconciler := mesh.NewReconciler(mesh.ReconcilerOptions{
		StatePath:  statePath,
		Applier:    applier,
		Routes:     routes,
		Output:     &dryOutput,
		DryRun:     true,
		Interface:  "wg0",
		MTU:        1420,
		ListenPort: 51820,
	})
	metrics := agentmetrics.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	identity, _, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pollOnce(context.Background(), identity, api, reconciler, metrics, logger); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if got := applier.calls.Load(); got != 1 {
		t.Fatalf("first poll applier calls = %d", got)
	}
	identity, _, _ = state.Load(statePath)
	if err := pollOnce(context.Background(), identity, api, reconciler, metrics, logger); err != nil {
		t.Fatalf("304 poll: %v", err)
	}
	if got := applier.calls.Load(); got != 1 {
		t.Fatalf("304 invoked applier %d times total", got)
	}
	request(t, app.control.Server.URL, "POST", "/api/v1/networks/"+networkID+"/routes", app.token, map[string]any{
		"node_id": nodeB.NodeID,
		"cidr":    "192.168.1.0/24",
		"enabled": true,
	}, nil)
	identity, _, _ = state.Load(statePath)
	if err := pollOnce(context.Background(), identity, api, reconciler, metrics, logger); err != nil {
		t.Fatalf("updated poll: %v", err)
	}
	if got := applier.calls.Load(); got != 2 {
		t.Fatalf("updated poll applier calls = %d, want one additional call", got)
	}
	output := dryOutput.String()
	if !strings.Contains(output, "peer ") || !strings.Contains(output, "allowed_ips=") {
		t.Fatalf("apply command list omitted peer/allowed_ips:\n%s", output)
	}
}

func TestRegistrationRetries401AndHeartbeatRetries5xx(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/heartbeat") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Server = server.URL
	cfg.Token = "invalid-registration-token"
	cfg.StatePath = statePath
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 1150*time.Millisecond)
	defer cancel()
	_, err := ensureIdentity(ctx, cfg, client.New(cfg.Server, cfg.Token), false, logger)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("registration retry ended with %v", err)
	}
	if _, exists, loadErr := state.Load(statePath); loadErr == nil && exists {
		t.Fatal("failed registration created state")
	}

	var failures atomic.Int64
	observer := heartbeatObserver{onHeartbeat: func(result string) {
		if result == "failure" {
			failures.Add(1)
		}
	}}
	heartbeatCtx, stopHeartbeat := context.WithTimeout(context.Background(), 1150*time.Millisecond)
	defer stopHeartbeat()
	client := client.New(server.URL, "heartbeat-token")
	heartbeat.Run(heartbeatCtx, time.Second, func(ctx context.Context) (time.Duration, error) {
		_, err := client.Heartbeat(ctx, "node", "test")
		return 0, err
	}, observer, logger)
	if failures.Load() < 2 {
		t.Fatalf("heartbeat failures observed = %d, want retry", failures.Load())
	}
}

type heartbeatObserver struct {
	onHeartbeat func(string)
}

func (o heartbeatObserver) Heartbeat(result string) {
	if o.onHeartbeat != nil {
		o.onHeartbeat(result)
	}
}

type meshRecordingExecutor struct {
	commands []route.Command
}

func (e *meshRecordingExecutor) Run(_ context.Context, command route.Command) error {
	e.commands = append(e.commands, command)
	return nil
}
func (e *meshRecordingExecutor) Output(context.Context, route.Command) (string, error) {
	return "", nil
}
