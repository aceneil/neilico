package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsYAMLAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	content := `server: https://control.example.test
node:
  name: yaml-node
  tags: ["yaml"]
mesh:
  interface: wg-test
  mtu: 1380
  listen_port: 51821
  cleanup_on_exit: false
proxy:
  enabled: false
metrics:
  enabled: false
  listen: 127.0.0.1:9101
log:
  level: debug
state_path: /tmp/custom-state.json
poll_interval: 15s
heartbeat_interval: 20s
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UMPP_AGENT_NODE_NAME", "env-node")
	t.Setenv("UMPP_AGENT_NODE_TAGS", "one, two")
	t.Setenv("UMPP_AGENT_POLL_INTERVAL", "45s")
	t.Setenv("UMPP_AGENT_MESH_ALLOW_FORWARDING", "true")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server != "https://control.example.test" || cfg.Node.Name != "env-node" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if len(cfg.Node.Tags) != 2 || cfg.Node.Tags[1] != "two" {
		t.Fatalf("tags = %#v", cfg.Node.Tags)
	}
	if cfg.Mesh.MTU != 1380 || cfg.Mesh.CleanupOnExit || !cfg.Mesh.AllowForwarding {
		t.Fatalf("mesh = %#v", cfg.Mesh)
	}
	if time.Duration(cfg.PollInterval) != 45*time.Second || time.Duration(cfg.HeartbeatInterval) != 20*time.Second {
		t.Fatalf("intervals = %s / %s", time.Duration(cfg.PollInterval), time.Duration(cfg.HeartbeatInterval))
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte("server: https://example.test\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted unknown field")
	}
}
