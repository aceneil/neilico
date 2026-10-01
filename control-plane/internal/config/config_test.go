package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAndEnvironmentOverrides(t *testing.T) {
	clearEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
server:
  host: 127.0.0.1
  port: 9090
database:
  driver: postgres
  dsn: postgres://localhost/umpp
auth:
  jwt_secret: test-secret-from-file
  access_ttl: 10m
  refresh_ttl: 2h
bootstrap:
  admin_email: admin@example.test
  admin_password: test-password
  default_tenant: main
node:
  heartbeat_timeout: 45s
proxy:
  enabled: false
  kind: nps
  listen: ":9081"
  nps:
    config_path: /tmp/nps.json
    binary_path: /opt/nps
    pid_file: /tmp/nps.pid
    reload_strategy: file
log:
  level: warn
  format: text
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 9090 {
		t.Fatalf("unexpected server config: %#v", cfg.Server)
	}
	if cfg.Auth.AccessTTL != 10*time.Minute || cfg.Auth.RefreshTTL != 2*time.Hour {
		t.Fatalf("unexpected TTLs: %#v", cfg.Auth)
	}
	if cfg.Node.HeartbeatTimeout != 45*time.Second {
		t.Fatalf("heartbeat timeout = %v", cfg.Node.HeartbeatTimeout)
	}

	t.Setenv("UMPP_SERVER_HOST", "0.0.0.0")
	t.Setenv("UMPP_SERVER_PORT", "8081")
	t.Setenv("UMPP_DATABASE_DRIVER", "sqlite")
	t.Setenv("UMPP_DATABASE_DSN", "file:test?mode=memory")
	t.Setenv("UMPP_AUTH_JWT_SECRET", "environment-secret-value")
	t.Setenv("UMPP_AUTH_ACCESS_TTL", "1m")
	t.Setenv("UMPP_AUTH_REFRESH_TTL", "24h")
	t.Setenv("UMPP_NODE_HEARTBEAT_TIMEOUT", "60s")
	t.Setenv("UMPP_LOG_LEVEL", "debug")
	t.Setenv("UMPP_LOG_FORMAT", "json")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() with environment error = %v", err)
	}
	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 8081 {
		t.Fatalf("environment server override failed: %#v", cfg.Server)
	}
	if cfg.Database.Driver != "sqlite" || cfg.Database.DSN != "file:test?mode=memory" {
		t.Fatalf("environment database override failed: %#v", cfg.Database)
	}
	if cfg.Auth.JWTSecret != "environment-secret-value" || cfg.Auth.AccessTTL != time.Minute {
		t.Fatalf("environment auth override failed: %#v", cfg.Auth)
	}
	if cfg.Node.HeartbeatTimeout != time.Minute || cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Fatalf("environment node/log override failed: %#v %#v", cfg.Node, cfg.Log)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	clearEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("database:\n  driver: sqlite\n  dsn: file:test\nnode:\n  heartbeat_timeout: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() expected invalid duration error")
	}
}

func clearEnvironment(t *testing.T) {
	t.Helper()
	keys := []string{
		"UMPP_SERVER_HOST", "UMPP_SERVER_PORT",
		"UMPP_DATABASE_DRIVER", "UMPP_DATABASE_DSN",
		"UMPP_AUTH_JWT_SECRET", "UMPP_AUTH_ACCESS_TTL", "UMPP_AUTH_REFRESH_TTL",
		"UMPP_BOOTSTRAP_ADMIN_EMAIL", "UMPP_BOOTSTRAP_ADMIN_PASSWORD", "UMPP_BOOTSTRAP_DEFAULT_TENANT",
		"UMPP_NODE_HEARTBEAT_TIMEOUT", "UMPP_LOG_LEVEL", "UMPP_LOG_FORMAT",
		"UMPP_PROXY_ENABLED", "UMPP_PROXY_KIND", "UMPP_PROXY_LISTEN",
		"UMPP_NPS_CONFIG_PATH", "UMPP_NPS_BINARY_PATH", "UMPP_NPS_PID_FILE", "UMPP_NPS_RELOAD_STRATEGY",
	}
	for _, key := range keys {
		value, existed := os.LookupEnv(key)
		t.Setenv(key, value)
		if !existed {
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}
