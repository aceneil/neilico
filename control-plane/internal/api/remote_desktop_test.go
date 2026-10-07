package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

// testRemoteDesktopPublicKey 是一个**公钥**字面量，用于确认接口下发公钥且绝不泄露私钥。
const testRemoteDesktopPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINEILICOTESTPUBLICKEY0000000000 neilico-rustdesk"

// newRemoteDesktopApp 用给定的公钥文件路径构造测试实例。
func newRemoteDesktopApp(t *testing.T, publicKeyFile string) testApp {
	t.Helper()
	return newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true,
		Kind:    "builtin",
		Listen:  "127.0.0.1:0",
		RemoteDesktop: api.RemoteDesktopOptions{
			Enabled:       true,
			IDServer:      "192.0.2.1",
			RelayServer:   "192.0.2.1",
			PublicKeyFile: publicKeyFile,
			Ports:         []int{21115, 21116, 21117, 21118, 21119},
		},
	})
}

func writePublicKey(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRemoteDesktopConfigRequiresAuthentication(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config", "", nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "unauthorized")

	// 登录后可读，且只下发公钥。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var cfg service.RemoteDesktopConfig
	decodeResponse(t, body, &cfg)
	if !cfg.Available || cfg.PublicKey != testRemoteDesktopPublicKey {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if len(cfg.Ports) != 5 {
		t.Fatalf("ports = %#v, want the five rustdesk-server ports", cfg.Ports)
	}
	if cfg.IDServer != "192.0.2.1" || cfg.RelayServer != "192.0.2.1" {
		t.Fatalf("unexpected server params: %#v", cfg)
	}
}

func TestRemoteDesktopConfigUnavailableWithoutPublicKey(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "id_ed25519.pub")
	app := newRemoteDesktopApp(t, missing)
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var cfg service.RemoteDesktopConfig
	decodeResponse(t, body, &cfg)
	if cfg.Available {
		t.Fatalf("available = true, want false when the public key file is missing: %#v", cfg)
	}
	if cfg.PublicKey != "" {
		t.Fatalf("public_key = %q, want empty when not available", cfg.PublicKey)
	}
	if strings.TrimSpace(cfg.Hint) == "" {
		t.Fatal("expected a non-empty hint explaining the server is not ready")
	}
	if len(cfg.Ports) != 5 {
		t.Fatalf("ports = %#v, want the five rustdesk-server ports", cfg.Ports)
	}
}

func TestRemoteDesktopConfigNeverLeaksPrivateKey(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "id_ed25519")
	// 私钥文件必须在任何情况下都不被读取/回显。
	if err := os.WriteFile(privatePath, []byte("PRIVATE-KEY-MUST-NOT-LEAK"), 0o600); err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(publicPath, []byte(testRemoteDesktopPublicKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newRemoteDesktopApp(t, publicPath)
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if strings.Contains(string(body), "PRIVATE-KEY-MUST-NOT-LEAK") {
		t.Fatalf("response leaked private key material: %s", body)
	}
	var cfg service.RemoteDesktopConfig
	decodeResponse(t, body, &cfg)
	if !cfg.Available || cfg.PublicKey != testRemoteDesktopPublicKey {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestRemoteDesktopUpdateRequiresPlatformAdmin(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": admin.User.TenantID,
		"email":     "ops@example.test",
		"password":  "ops-password",
		"role":      auth.RoleOps,
		"status":    "active",
	})
	requireStatus(t, status, http.StatusCreated)

	ops := mustLogin(t, app, "ops@example.test", "ops-password")

	// 未登录 → 401。
	status, _ = mustRequest(t, app.server, http.MethodPut, "/api/v1/remote-desktop/config", "", map[string]any{"id_server": "10.0.0.1"})
	requireStatus(t, status, http.StatusUnauthorized)

	// 非 admin → 403。
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/remote-desktop/config", ops.Token, map[string]any{"id_server": "10.0.0.1"})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")

	// 只读用户可读，但读不到权限问题的字段值改动。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config", ops.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var cfg service.RemoteDesktopConfig
	decodeResponse(t, body, &cfg)
	if cfg.IDServer != "192.0.2.1" {
		t.Fatalf("config changed by a non-admin: %#v", cfg)
	}
}

func TestRemoteDesktopUpdateAppliesAndIsAudited(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPut, "/api/v1/remote-desktop/config", admin.Token, map[string]any{
		"enabled":      false,
		"id_server":    "10.10.10.10:21116",
		"relay_server": "10.10.10.11:21117",
	})
	requireStatus(t, status, http.StatusOK)
	var cfg service.RemoteDesktopConfig
	decodeResponse(t, body, &cfg)
	if cfg.Enabled || cfg.IDServer != "10.10.10.10:21116" || cfg.RelayServer != "10.10.10.11:21117" {
		t.Fatalf("update was not applied: %#v", cfg)
	}

	// GET 反映改动。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &cfg)
	if cfg.Enabled || cfg.IDServer != "10.10.10.10:21116" {
		t.Fatalf("config did not persist the change: %#v", cfg)
	}

	// 非法值 → 400，且不改动已有配置。
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/remote-desktop/config", admin.Token, map[string]any{"id_server": "bad host"})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")

	// 沿用现有审计：改动写审计日志。
	var audits int64
	if err := app.db.Model(&models.AuditLog{}).Where("action = ?", "remote_desktop.config.update").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits < 1 {
		t.Fatalf("remote desktop config audit records = %d, want at least 1", audits)
	}
}

func TestRemoteDesktopDevicesIncludeConnectionParams(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", admin.Token, map[string]any{
		"name": "nas-01", "public_key": "k1", "os": "linux", "arch": "amd64", "version": "0.1.0",
		"tags": []string{"home", "rustdesk:123456789"},
	})
	requireStatus(t, status, http.StatusCreated)
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", admin.Token, map[string]any{
		"name": "pc-02", "public_key": "k2", "os": "windows", "arch": "amd64", "version": "0.1.0",
		"tags": []string{},
	})
	requireStatus(t, status, http.StatusCreated)

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/devices", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.RemoteDesktopDeviceList
	decodeResponse(t, body, &list)
	if list.Total != 2 {
		t.Fatalf("device total = %d, want 2", list.Total)
	}

	byName := map[string]service.RemoteDesktopDevice{}
	for _, device := range list.Items {
		byName[device.Name] = device
	}
	reporting := byName["nas-01"]
	if reporting.RustdeskID != "123456789" || reporting.ConnectURL != "rustdesk://123456789" {
		t.Fatalf("unexpected rustdesk hint: %#v", reporting)
	}
	if reporting.Platform != "linux/amd64" {
		t.Fatalf("platform = %q, want linux/amd64", reporting.Platform)
	}
	if !strings.Contains(reporting.ConnectionParams, testRemoteDesktopPublicKey) {
		t.Fatalf("connection params missing public key: %q", reporting.ConnectionParams)
	}
	if !strings.Contains(reporting.ConnectionParams, "192.0.2.1") {
		t.Fatalf("connection params missing server: %q", reporting.ConnectionParams)
	}

	missing := byName["pc-02"]
	if missing.RustdeskID != "" || missing.ConnectURL != "" || missing.RustdeskHint != "" {
		t.Fatalf("device without a reported ID must have an empty hint: %#v", missing)
	}
	if !strings.Contains(missing.ConnectionParams, "未上报") {
		t.Fatalf("connection params should explain the missing ID: %q", missing.ConnectionParams)
	}
}

func TestRemoteDesktopStatusProbeIsBounded(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	started := time.Now()
	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/status", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("status probe took %s, want well under 3s budget", elapsed)
	}
	var probe service.RemoteDesktopStatus
	decodeResponse(t, body, &probe)
	if len(probe.Ports) != 3 {
		t.Fatalf("probed ports = %#v, want 3", probe.Ports)
	}
	for _, port := range probe.Ports {
		if port.Port == 0 || strings.TrimSpace(port.Target) == "" {
			t.Fatalf("incomplete probe result: %#v", port)
		}
	}
}
