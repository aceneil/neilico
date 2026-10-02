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
  dashboard_dir: /srv/dashboard
  dashboard_spa: false
database:
  driver: postgres
  dsn: postgres://localhost/neilico
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
	if cfg.Server.DashboardDir != "/srv/dashboard" || cfg.Server.DashboardSPA {
		t.Fatalf("unexpected dashboard config: %#v", cfg.Server)
	}
	if cfg.Auth.AccessTTL != 10*time.Minute || cfg.Auth.RefreshTTL != 2*time.Hour {
		t.Fatalf("unexpected TTLs: %#v", cfg.Auth)
	}
	if cfg.Node.HeartbeatTimeout != 45*time.Second {
		t.Fatalf("heartbeat timeout = %v", cfg.Node.HeartbeatTimeout)
	}

	t.Setenv("NEILICO_SERVER_HOST", "0.0.0.0")
	t.Setenv("NEILICO_SERVER_PORT", "8081")
	t.Setenv("NEILICO_SERVER_DASHBOARD_DIR", "/opt/dashboard")
	t.Setenv("NEILICO_SERVER_DASHBOARD_SPA", "true")
	t.Setenv("NEILICO_DATABASE_DRIVER", "sqlite")
	t.Setenv("NEILICO_DATABASE_DSN", "file:test?mode=memory")
	t.Setenv("NEILICO_AUTH_JWT_SECRET", "environment-secret-value")
	t.Setenv("NEILICO_AUTH_ACCESS_TTL", "1m")
	t.Setenv("NEILICO_AUTH_REFRESH_TTL", "24h")
	t.Setenv("NEILICO_NODE_HEARTBEAT_TIMEOUT", "60s")
	t.Setenv("NEILICO_LOG_LEVEL", "debug")
	t.Setenv("NEILICO_LOG_FORMAT", "json")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() with environment error = %v", err)
	}
	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 8081 {
		t.Fatalf("environment server override failed: %#v", cfg.Server)
	}
	if cfg.Server.DashboardDir != "/opt/dashboard" || !cfg.Server.DashboardSPA {
		t.Fatalf("environment dashboard override failed: %#v", cfg.Server)
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
		"NEILICO_SERVER_HOST", "NEILICO_SERVER_PORT",
		"NEILICO_SERVER_DASHBOARD_DIR", "NEILICO_SERVER_DASHBOARD_SPA",
		"NEILICO_DATABASE_DRIVER", "NEILICO_DATABASE_DSN",
		"NEILICO_AUTH_JWT_SECRET", "NEILICO_AUTH_ACCESS_TTL", "NEILICO_AUTH_REFRESH_TTL",
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL", "NEILICO_BOOTSTRAP_ADMIN_PASSWORD", "NEILICO_BOOTSTRAP_DEFAULT_TENANT",
		"NEILICO_NODE_HEARTBEAT_TIMEOUT", "NEILICO_LOG_LEVEL", "NEILICO_LOG_FORMAT",
		"NEILICO_RATELIMIT_ENABLED", "NEILICO_RATELIMIT_RPS", "NEILICO_RATELIMIT_BURST",
		"NEILICO_PROXY_ENABLED", "NEILICO_PROXY_KIND", "NEILICO_PROXY_LISTEN",
		"NEILICO_NPS_CONFIG_PATH", "NEILICO_NPS_BINARY_PATH", "NEILICO_NPS_PID_FILE", "NEILICO_NPS_RELOAD_STRATEGY",
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

func TestRateLimitDefaultsEnvironmentAndValidation(t *testing.T) {
	clearEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("database:\n  driver: sqlite\n  dsn: file:test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RateLimit.Enabled || cfg.RateLimit.RPS != 20 || cfg.RateLimit.Burst != 40 {
		t.Fatalf("unexpected rate limit defaults: %#v", cfg.RateLimit)
	}
	t.Setenv("NEILICO_RATELIMIT_ENABLED", "false")
	t.Setenv("NEILICO_RATELIMIT_RPS", "3.5")
	t.Setenv("NEILICO_RATELIMIT_BURST", "7")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.Enabled || cfg.RateLimit.RPS != 3.5 || cfg.RateLimit.Burst != 7 {
		t.Fatalf("rate limit environment override failed: %#v", cfg.RateLimit)
	}
	cfg.RateLimit.RPS = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted zero rate limit")
	}
}
