package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
)

// stopRemoteDesktopServer 在测试结束时停止监管器拉起的（假）子进程，避免泄漏。
type remoteDesktopStopper interface{ StopRemoteDesktopServer() }

func newRemoteDesktopServerApp(t *testing.T, mode string) testApp {
	t.Helper()
	keyDir := t.TempDir()
	// 假二进制：常驻不退出（忽略传参），足以验证「拉起/停止」的生命周期语义。
	fake := filepath.Join(t.TempDir(), "fake-hb")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true,
		Kind:    "builtin",
		Listen:  "127.0.0.1:0",
		RemoteDesktop: api.RemoteDesktopOptions{
			Enabled:     true,
			IDServer:    "192.0.2.1",
			RelayServer: "192.0.2.1",
			Ports:       []int{21115, 21116, 21117, 21118, 21119},
			ServerMode:  mode,
			KeyDir:      keyDir,
			HBBSPath:    fake,
			HBBRPath:    fake,
		},
	})
	if stopper, ok := app.handler.(remoteDesktopStopper); ok {
		t.Cleanup(stopper.StopRemoteDesktopServer)
	}
	return app
}

func TestRemoteDesktopServerStatusAndToggle(t *testing.T) {
	app := newRemoteDesktopServerApp(t, "on_demand")
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	// 初始：未运行、模式 on_demand、无监听。
	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/server-status", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var view struct {
		Mode           string `json:"mode"`
		Running        bool   `json:"running"`
		ListeningPorts []int  `json:"listening_ports"`
	}
	decodeResponse(t, body, &view)
	if view.Running || view.Mode != "on_demand" || len(view.ListeningPorts) != 0 {
		t.Fatalf("unexpected initial status: %+v", view)
	}

	// 未登录 → 401。
	status, _ = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/server-status", "", nil)
	requireStatus(t, status, http.StatusUnauthorized)

	// admin 手动 start → 200 且 running=true。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/server/start", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &view)
	if !view.Running {
		t.Fatalf("expected running after manual start, got %+v", view)
	}

	// 再查确认运行中。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/server-status", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &view)
	if !view.Running {
		t.Fatalf("expected running after start, got %+v", view)
	}

	// admin 手动 stop → 200 且 running=false。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/server/stop", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &view)
	if view.Running {
		t.Fatalf("expected stopped after manual stop, got %+v", view)
	}

	// 未知动作 → 404。
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/server/bogus", admin.Token, nil)
	requireStatus(t, status, http.StatusNotFound)
}

func TestRemoteDesktopServerToggleRequiresAdmin(t *testing.T) {
	app := newRemoteDesktopServerApp(t, "on_demand")
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": admin.User.TenantID,
		"email":     "ops@example.test",
		"password":  "ops-password",
		"role":      auth.RoleOps,
		"status":    "active",
	})
	requireStatus(t, status, http.StatusCreated)
	ops := mustLogin(t, app, "ops@example.test", "ops-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/server/start", ops.Token, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")
}

func TestRemoteDesktopServerOffModeRejectsStart(t *testing.T) {
	app := newRemoteDesktopServerApp(t, "off")
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/server/start", admin.Token, nil)
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "server_disabled")

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/server-status", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if !strings.Contains(string(body), `"mode":"off"`) || !strings.Contains(string(body), `"running":false`) {
		t.Fatalf("off-mode status mismatch: %s", body)
	}
}
