package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
	"neilico/control-plane/internal/service/proxy"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

type testApp struct {
	t       *testing.T
	server  *httptest.Server
	db      *gormDBAlias
	sweeper *service.NodeSweeper
	handler http.Handler
	proxy   *proxy.Builtin
}

type gormDBAlias = gormDB

type gormDB = gorm.DB

type loginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID       uuid.UUID `json:"id"`
		Email    string    `json:"email"`
		Role     string    `json:"role"`
		TenantID uuid.UUID `json:"tenant_id"`
	} `json:"user"`
}

func TestCompleteControlPlaneFlow(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	if admin.User.Email != "admin@example.test" || admin.User.Role != auth.RolePlatformAdmin {
		t.Fatalf("unexpected bootstrap admin: %#v", admin.User)
	}
	if admin.Token == "" || admin.RefreshToken == "" {
		t.Fatal("login response omitted tokens")
	}

	status, refreshed := mustRequest(t, app.server, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": admin.RefreshToken})
	requireStatus(t, status, http.StatusOK)
	var refresh loginResponse
	decodeResponse(t, refreshed, &refresh)
	if refresh.Token == "" || refresh.Token == admin.Token {
		t.Fatal("refresh did not issue a new access token")
	}

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", "", nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "unauthorized")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "acme", "plan": "pro"})
	requireStatus(t, status, http.StatusCreated)
	var tenant models.Tenant
	decodeResponse(t, body, &tenant)

	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/tenants/"+tenant.ID.String(), admin.Token, map[string]any{"name": "acme-updated", "plan": "pro"})
	requireStatus(t, status, http.StatusOK)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": tenant.ID,
		"email":     "ops@acme.test",
		"password":  "tenant-admin-password",
		"role":      auth.RoleTenantAdmin,
		"status":    "active",
	})
	requireStatus(t, status, http.StatusCreated)
	var tenantAdmin models.User
	decodeResponse(t, body, &tenantAdmin)
	if tenantAdmin.TenantID != tenant.ID {
		t.Fatalf("user tenant = %s, want %s", tenantAdmin.TenantID, tenant.ID)
	}

	adminLogin := mustLogin(t, app, "ops@acme.test", "tenant-admin-password")
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", adminLogin.Token, map[string]any{
		"name":       "nas-01",
		"public_key": "test-wireguard-public-key",
		"os":         "linux",
		"arch":       "amd64",
		"version":    "0.1.0",
		"tags":       []string{"home", "nas"},
	})
	requireStatus(t, status, http.StatusCreated)
	var registered struct {
		NodeID     uuid.UUID `json:"node_id"`
		AgentToken string    `json:"agent_token"`
		TenantID   uuid.UUID `json:"tenant_id"`
		Status     string    `json:"status"`
	}
	decodeResponse(t, body, &registered)
	if registered.NodeID == uuid.Nil || registered.AgentToken == "" || registered.TenantID != tenant.ID || registered.Status != service.NodeStatusOffline {
		t.Fatalf("unexpected node registration: %#v", registered)
	}

	var stored models.Node
	if err := app.db.First(&stored, "id = ?", registered.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AgentTokenHash == registered.AgentToken || !auth.AgentTokenEqual(stored.AgentTokenHash, registered.AgentToken) {
		t.Fatal("agent token was not stored only as a hash")
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+registered.NodeID.String()+"/heartbeat", registered.AgentToken, map[string]any{"version": "0.2.0"})
	requireStatus(t, status, http.StatusOK)
	var heartbeat map[string]any
	decodeResponse(t, body, &heartbeat)
	if heartbeat["ok"] != true || heartbeat["next_heartbeat_seconds"] != float64(30) || heartbeat["server_time"] == nil {
		t.Fatalf("unexpected heartbeat response: %#v", heartbeat)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes?status=online&tag=home", adminLogin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.NodeList
	decodeResponse(t, body, &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != registered.NodeID {
		t.Fatalf("unexpected node list: %#v", list)
	}
	if list.Items[0].Status != service.NodeStatusOnline || list.Items[0].LastSeen == nil {
		t.Fatalf("heartbeat was not reflected: %#v", list.Items[0])
	}
	if list.Items[0].Version != "0.2.0" {
		t.Fatalf("heartbeat version = %q, want 0.2.0", list.Items[0].Version)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+registered.NodeID.String()+"/heartbeat", registered.AgentToken+"forged", map[string]any{"version": "0.3.0"})
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_agent_token")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "other", "plan": "free"})
	requireStatus(t, status, http.StatusCreated)
	var otherTenant models.Tenant
	decodeResponse(t, body, &otherTenant)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID,
		"email":     "ops@other.test",
		"password":  "other-password",
		"role":      auth.RoleTenantAdmin,
	})
	requireStatus(t, status, http.StatusCreated)
	otherLogin := mustLogin(t, app, "ops@other.test", "other-password")
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+registered.NodeID.String(), otherLogin.Token, nil)
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("cross-tenant node status = %d body = %s", status, body)
	}
	if bytes.Contains(body, []byte("nas-01")) || bytes.Contains(body, []byte(registered.AgentToken)) {
		t.Fatal("cross-tenant response leaked node details")
	}

	expiredAt := time.Now().UTC().Add(-2 * time.Minute)
	result := app.db.Model(&models.Node{}).Where("id = ?", registered.NodeID).Updates(map[string]any{
		"last_seen": expiredAt,
		"status":    service.NodeStatusOnline,
	})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	swept, err := app.sweeper.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("sweeper error = %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept nodes = %d, want 1", swept)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes?status=offline", adminLogin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &list)
	if list.Total != 1 || list.Items[0].Status != service.NodeStatusOffline {
		t.Fatalf("node was not marked offline: %#v", list)
	}

	var registerAudits int64
	if err := app.db.Model(&models.AuditLog{}).Where("action = ? AND resource = ?", http.MethodPost, "/api/v1/nodes/register").Count(&registerAudits).Error; err != nil {
		t.Fatal(err)
	}
	if registerAudits != 1 {
		t.Fatalf("register audit records = %d, want 1", registerAudits)
	}
	var heartbeatAudits int64
	if err := app.db.Model(&models.AuditLog{}).Where("action = ? AND resource = ?", http.MethodPost, "/api/v1/nodes/"+registered.NodeID.String()+"/heartbeat").Count(&heartbeatAudits).Error; err != nil {
		t.Fatal(err)
	}
	if heartbeatAudits < 2 {
		t.Fatalf("heartbeat audit records = %d, want at least 2", heartbeatAudits)
	}

	status, body = mustRequest(t, app.server, http.MethodDelete, "/api/v1/nodes/"+registered.NodeID.String(), adminLogin.Token, nil)
	requireStatus(t, status, http.StatusNoContent)
	status, _ = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+registered.NodeID.String(), adminLogin.Token, nil)
	requireStatus(t, status, http.StatusNotFound)

	status, body = mustRequest(t, app.server, http.MethodGet, "/metrics", "", nil)
	requireStatus(t, status, http.StatusOK)
	if !bytes.Contains(body, []byte("neilico_nodes_online")) || !bytes.Contains(body, []byte("neilico_http_requests_total")) {
		t.Fatalf("metrics output missing required metrics:\n%s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/healthz", "", nil)
	requireStatus(t, status, http.StatusOK)
	var health map[string]any
	decodeResponse(t, body, &health)
	if health["status"] != "ok" || health["db"] != "up" || health["version"] == nil || health["uptime"] == nil {
		t.Fatalf("unexpected health response: %#v", health)
	}
}

func newTestApp(t *testing.T) testApp {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn}, "error")
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatalf("db.AutoMigrate() error = %v", err)
	}
	bootstrap := config.Bootstrap{
		AdminEmail:    "admin@example.test",
		AdminPassword: "bootstrap-password",
		DefaultTenant: "bootstrap",
	}
	if err := service.Bootstrap(context.Background(), handle, bootstrap); err != nil {
		t.Fatalf("service.Bootstrap() error = %v", err)
	}
	manager, err := auth.NewManager(testJWTSecret, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	nodeService := service.NewNodeService(handle, time.Minute)
	sweeper := service.NewNodeSweeper(handle, time.Minute, logger)
	promMetrics := metrics.New(handle)
	builtinProxy := proxy.NewBuiltin(handle, manager, logger, promMetrics)
	handler := api.NewWithProxy(handle, manager, nodeService, promMetrics, logger, "test", builtinProxy, api.ProxyOptions{
		Enabled: true,
		Kind:    "builtin",
		Listen:  "127.0.0.1:0",
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return testApp{t: t, server: server, db: handle, sweeper: sweeper, handler: handler, proxy: builtinProxy}
}

func mustLogin(t *testing.T, app testApp, email, password string) loginResponse {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/auth/login", "", map[string]any{"email": email, "password": password})
	requireStatus(t, status, http.StatusOK)
	var response loginResponse
	decodeResponse(t, body, &response)
	return response
}

func mustRequest(t *testing.T, server *httptest.Server, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

func decodeResponse(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}
}

func requireStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func requireErrorCode(t *testing.T, body []byte, code string) {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, body, &payload)
	if payload.Error.Code != code {
		t.Fatalf("error code = %q, want %q (%s)", payload.Error.Code, code, strings.TrimSpace(string(body)))
	}
}
