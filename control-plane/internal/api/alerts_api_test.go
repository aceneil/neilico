package api_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"umpp/control-plane/internal/api"
	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/metrics"
	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service"
	alerts "umpp/control-plane/internal/service/alerts"
	alertservice "umpp/control-plane/internal/service/alerts"
	"umpp/control-plane/internal/service/proxy"
)

func TestAlertsAPIEvaluationSummaryIsolationAndRBAC(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	tenantA, opsA := createM4BUser(t, app, admin.Token, "alerts-a", "ops-a@example.test", auth.RoleOps)
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": tenantA, "email": "readonly-a@example.test", "password": "readonly-password",
		"role": auth.RoleReadonly, "status": "active",
	})
	requireStatus(t, status, http.StatusCreated)
	readonlyA := mustLogin(t, app, "readonly-a@example.test", "readonly-password")
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/alerts/evaluate", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	tenantB, opsB := createM4BUser(t, app, admin.Token, "alerts-b", "ops-b@example.test", auth.RoleOps)

	nodeA := createAlertNode(t, app, tenantA, "offline-a", time.Now().UTC().Add(-10*time.Minute))
	nodeB := createAlertNode(t, app, tenantB, "offline-b", time.Now().UTC().Add(-10*time.Minute))
	expiringA := createAlertCertificate(t, app, tenantA, "expiring-a.example.test", "active", "", timePointer(time.Now().UTC().Add(10*24*time.Hour)))
	failedA := createAlertCertificate(t, app, tenantA, "failed-a.example.test", "failed", "ACME order failed", nil)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/alerts/evaluate", opsA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var evaluated struct {
		Items        []alertservice.Alert `json:"items"`
		Total        int                  `json:"total"`
		Insufficient []alertservice.Alert `json:"insufficient_data"`
	}
	decodeResponse(t, body, &evaluated)
	if evaluated.Total != 3 || len(evaluated.Items) != 3 {
		t.Fatalf("tenant A evaluation = %s", body)
	}
	if len(evaluated.Insufficient) != 2 {
		t.Fatalf("insufficient data results = %s", body)
	}
	seenRules := map[string]bool{}
	for _, alert := range evaluated.Items {
		seenRules[alert.Rule] = true
		if alert.State != alertservice.StateFiring {
			t.Fatalf("evaluated alert is not firing: %#v", alert)
		}
	}
	for _, rule := range []string{alertservice.RuleNodeOffline, alertservice.RuleCertificateExpiring, alertservice.RuleConfigDispatchFailed} {
		if !seenRules[rule] {
			t.Fatalf("evaluation omitted rule %s: %s", rule, body)
		}
	}
	for _, alert := range evaluated.Insufficient {
		if alert.DataStatus != alertservice.DataStatusInsufficientData || !strings.Contains(alert.Detail, "insufficient_data") {
			t.Fatalf("data source missing but not marked insufficient: %#v", alert)
		}
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts?state=firing", opsA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list alertservice.List
	decodeResponse(t, body, &list)
	if list.Total != 3 || len(list.Items) != 3 {
		t.Fatalf("tenant A alert list = %s", body)
	}
	var expiringAlert alertservice.Alert
	for _, alert := range list.Items {
		if alert.Rule == alertservice.RuleCertificateExpiring {
			expiringAlert = alert
		}
	}
	if expiringAlert.ID == "" || expiringAlert.TargetID != expiringA.ID.String() || expiringAlert.Severity != alertservice.SeverityWarning {
		t.Fatalf("expiring alert = %#v", expiringAlert)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts/summary", opsA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var summary alertservice.Summary
	decodeResponse(t, body, &summary)
	if summary.Firing.Critical != 1 || summary.Firing.Warning != 2 || summary.Firing.Info != 0 {
		t.Fatalf("tenant A summary = %s", body)
	}
	if summary.ByRule[alertservice.RuleNodeOffline] != 1 || summary.ByRule[alertservice.RuleCertificateExpiring] != 1 || summary.ByRule[alertservice.RuleConfigDispatchFailed] != 1 {
		t.Fatalf("summary by rule = %#v", summary.ByRule)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts/rules", readonlyA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var rules struct {
		Items []alertservice.Rule `json:"items"`
		Total int                 `json:"total"`
	}
	decodeResponse(t, body, &rules)
	if rules.Total != 5 || len(rules.Items) != 5 {
		t.Fatalf("rules response = %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts/"+expiringAlert.ID, readonlyA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var detail struct {
		Alert  alertservice.Alert        `json:"alert"`
		Events []alertservice.AlertEvent `json:"events"`
	}
	decodeResponse(t, body, &detail)
	if detail.Alert.ID != expiringAlert.ID || len(detail.Events) != 1 || detail.Events[0].State != alertservice.StateFiring {
		t.Fatalf("alert detail = %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/alerts/evaluate", readonlyA.Token, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts/evaluate", opsA.Token, nil)
	requireStatus(t, status, http.StatusMethodNotAllowed)

	_, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/alerts/evaluate", opsB.Token, nil)
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts?target_id="+nodeB.ID.String(), opsA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &list)
	if list.Total != 0 {
		t.Fatalf("cross-tenant target leaked to tenant A: %s", body)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts?tenant_id="+tenantB.String()+"&target_id="+nodeB.ID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &list)
	if list.Total != 1 || list.Items[0].TargetID != nodeB.ID.String() {
		t.Fatalf("platform admin tenant filter = %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/alerts?severity=warning&rule="+alertservice.RuleNodeOffline, opsA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &list)
	if list.Total != 1 || list.Items[0].TargetID != nodeA.ID.String() {
		t.Fatalf("filtered alert list = %s", body)
	}
	if failedA.ID == uuid.Nil {
		t.Fatal("failed certificate fixture missing")
	}
}

func TestMetricsExposeV1R2RequiredNames(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	status, body := mustRequest(t, app.server, http.MethodGet, "/metrics", "", nil)
	requireStatus(t, status, http.StatusOK)
	for _, name := range []string{
		"umpp_nodes_online",
		"umpp_tunnel_up",
		"umpp_p2p_success_rate",
		"umpp_relay_bytes",
		"umpp_proxy_requests",
		"umpp_config_version",
		"umpp_agent_heartbeat_latency",
		"umpp_alerts_firing",
	} {
		if !bytes.Contains(body, []byte(name)) {
			t.Fatalf("metrics output missing %s:\n%s", name, body)
		}
	}
	if !bytes.Contains(body, []byte(`umpp_p2p_success_rate 0`)) ||
		!bytes.Contains(body, []byte(`umpp_relay_bytes 0`)) ||
		!bytes.Contains(body, []byte(`umpp_agent_heartbeat_latency 0`)) {
		t.Fatalf("missing-data gauges are not explicit zero:\n%s", body)
	}
}

func createAlertNode(t *testing.T, app testApp, tenantID uuid.UUID, name string, lastSeen time.Time) models.Node {
	t.Helper()
	value := lastSeen.UTC()
	node := models.Node{
		ID: uuid.New(), TenantID: tenantID, Name: name, PublicKey: "alert-public-" + name,
		OS: "linux", Arch: "amd64", Version: "test", Status: "offline", LastSeen: &value,
		Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: uuid.NewString() + strings.Repeat("0", 32),
		CreatedAt: time.Now().UTC(),
	}
	if err := app.db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	return node
}

func createAlertCertificate(t *testing.T, app testApp, tenantID uuid.UUID, domain, status, lastError string, expiresAt *time.Time) models.Certificate {
	t.Helper()
	var expiry *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC()
		expiry = &value
	}
	item := models.Certificate{
		ID: uuid.New(), TenantID: tenantID, Domain: domain, Issuer: "acme",
		ExpiresAt: expiry, Status: status, LastError: lastError,
		AutoRenew: true, CreatedAt: time.Now().UTC(),
	}
	if err := app.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

func TestAlertWebhookNotificationIntegration(t *testing.T) {
	var calls atomic.Int32
	var received atomic.Value
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		payload, _ := io.ReadAll(r.Body)
		received.Store(payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	app := newTestAppWithAlertOptions(t, alerts.Options{
		WebhookURL: receiver.URL, WebhookTimeout: time.Second, WebhookRetries: 2,
	})
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	tenantID, ops := createM4BUser(t, app, admin.Token, "alert-webhook", "webhook-ops@example.test", auth.RoleOps)
	createAlertNode(t, app, tenantID, "webhook-offline", time.Now().UTC().Add(-time.Hour))

	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/alerts/evaluate", ops.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if calls.Load() != 2 {
		t.Fatalf("webhook calls = %d, want initial attempt plus one retry", calls.Load())
	}
	raw, ok := received.Load().([]byte)
	if !ok || len(raw) == 0 {
		t.Fatal("webhook receiver did not capture payload")
	}
	var notification alerts.Notification
	decodeResponse(t, raw, &notification)
	if notification.Event != "alert.firing" || notification.Schema != "umpp.alert.v1" || notification.Alert.Rule != alerts.RuleNodeOffline {
		t.Fatalf("webhook notification = %s", raw)
	}
	if bytes.Contains(raw, []byte("PRIVATE KEY")) || bytes.Contains(raw, []byte("agent_token")) {
		t.Fatalf("webhook leaked sensitive material: %s", raw)
	}
}

func newTestAppWithAlertOptions(t *testing.T, options alerts.Options) testApp {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(context.Background(), handle, config.Bootstrap{
		AdminEmail: "admin@example.test", AdminPassword: "bootstrap-password", DefaultTenant: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(testJWTSecret, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	nodeService := service.NewNodeService(handle, time.Minute)
	promMetrics := metrics.New(handle)
	builtinProxy := proxy.NewBuiltin(handle, manager, logger, promMetrics)
	handler := api.NewWithProxy(handle, manager, nodeService, promMetrics, logger, "test", builtinProxy, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0", Alerts: options,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return testApp{t: t, server: server, db: handle, handler: handler, proxy: builtinProxy}
}
