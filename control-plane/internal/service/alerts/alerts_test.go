package alerts

import (
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
	"gorm.io/gorm"

	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
)

func TestRuleThresholdBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	threshold := 5 * time.Minute
	at := now.Add(-threshold)
	before := now.Add(-threshold + time.Nanosecond)
	after := now.Add(-threshold - time.Nanosecond)
	nodes := []nodeObservation{
		{ID: "equal", Name: "equal", LastSeen: &at},
		{ID: "less", Name: "less", LastSeen: &before},
		{ID: "more", Name: "more", LastSeen: &after},
	}
	got := nodeRuleAlerts(nodes, now, threshold)
	if len(got) != 1 || got[0].TargetID != "more" {
		t.Fatalf("node boundary alerts = %#v", got)
	}

	expiringIn := 30 * 24 * time.Hour
	criticalIn := 7 * 24 * time.Hour
	expiringAt := now.Add(expiringIn)
	insideAt := now.Add(expiringIn - time.Nanosecond)
	criticalAt := now.Add(criticalIn)
	certs := []certificateObservation{
		{ID: "equal", Domain: "equal.test", Issuer: "test", Status: "active", ExpiresAt: &expiringAt},
		{ID: "inside", Domain: "inside.test", Issuer: "acme", Status: "active", ExpiresAt: &insideAt},
		{ID: "critical", Domain: "critical.test", Issuer: "acme", Status: "active", ExpiresAt: &criticalAt},
		{ID: "inactive", Domain: "inactive.test", Issuer: "acme", Status: "revoked", ExpiresAt: &insideAt},
	}
	got = certificateRuleAlerts(certs, now, expiringIn, criticalIn)
	if len(got) != 2 || got[0].TargetID != "inside" || got[0].Severity != SeverityWarning || got[1].Severity != SeverityCritical {
		t.Fatalf("certificate boundary alerts = %#v", got)
	}

	minimum := 0.60
	atMinimum := minimum
	belowMinimum := minimum - 0.0001
	if alert, firing := p2pRuleAlert(&atMinimum, now, minimum); firing || alert.DataStatus != "" {
		t.Fatalf("P2P at threshold fired: %#v", alert)
	}
	if alert, firing := p2pRuleAlert(&belowMinimum, now, minimum); !firing || alert.Value != belowMinimum {
		t.Fatalf("P2P below threshold did not fire: %#v", alert)
	}
	if alert, firing := p2pRuleAlert(nil, now, minimum); firing || alert.DataStatus != DataStatusInsufficientData || !strings.Contains(alert.Detail, "insufficient_data") {
		t.Fatalf("P2P missing data result = %#v", alert)
	}

	current := 300.0
	baseline := 100.0
	justBelow := 299.999
	if alert, firing := relayTrafficSpikeAlert(&current, &baseline, now, 3); !firing || alert.Threshold != 300 {
		t.Fatalf("relay at multiplier did not fire: %#v", alert)
	}
	if alert, firing := relayTrafficSpikeAlert(&justBelow, &baseline, now, 3); firing || alert.DataStatus != "" {
		t.Fatalf("relay below multiplier fired: %#v", alert)
	}
	if alert, firing := relayTrafficSpikeAlert(nil, &baseline, now, 3); firing || alert.DataStatus != DataStatusInsufficientData {
		t.Fatalf("relay missing data result = %#v", alert)
	}

	failedAt := now
	failed := []certificateObservation{
		{ID: "failed", Domain: "failed.test", Status: "failed", ExpiresAt: &failedAt},
		{ID: "last-error", Domain: "error.test", Status: "active", LastError: "renewal failed", ExpiresAt: &failedAt},
		{ID: "healthy", Domain: "healthy.test", Status: "active", ExpiresAt: &failedAt},
	}
	if got := configFailureAlerts(failed, now); len(got) != 2 || got[0].Rule != RuleConfigDispatchFailed || got[1].Rule != RuleConfigDispatchFailed {
		t.Fatalf("config failure alerts = %#v", got)
	}
}

func TestEngineStateMachineDedupesSinceAndEvents(t *testing.T) {
	handle := newAlertTestDB(t)
	tenantID := uuid.New()
	nodeID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "alerts-state", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	lastSeen := time.Now().UTC().Add(-time.Hour)
	if err := handle.Create(&models.Node{
		ID: nodeID, TenantID: tenantID, Name: "offline-node", PublicKey: "public",
		OS: "linux", Arch: "amd64", Version: "test", Status: "offline", LastSeen: &lastSeen,
		Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: strings.Repeat("a", 64), CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &recordingNotifier{}
	engine := NewEngine(handle, Options{NodeOfflineAfter: 5 * time.Minute}, recorder, nil, testLogger())

	first := findRuleAlert(t, engine.Evaluate(context.Background(), tenantID), RuleNodeOffline)
	if first.State != StateFiring || first.StartedAt == nil {
		t.Fatalf("first evaluation = %#v", first)
	}
	startedAt := *first.StartedAt
	for i := 0; i < 3; i++ {
		next := findRuleAlert(t, engine.Evaluate(context.Background(), tenantID), RuleNodeOffline)
		if next.State != StateFiring || !next.Since.After(first.Since) {
			t.Fatalf("continuous evaluation %d = %#v", i+2, next)
		}
		if next.StartedAt == nil || !next.StartedAt.Equal(startedAt) {
			t.Fatalf("continuous evaluation changed started_at: %#v", next)
		}
		first = next
	}
	if count := alertEventCount(t, handle, first.ID); count != 1 {
		t.Fatalf("continuous event count = %d, want 1", count)
	}

	now := time.Now().UTC()
	if err := handle.Model(&models.Node{}).Where("id = ?", nodeID).Update("last_seen", now).Error; err != nil {
		t.Fatal(err)
	}
	resolved := findRuleAlert(t, engine.Evaluate(context.Background(), tenantID), RuleNodeOffline)
	if resolved.State != StateResolved || resolved.ResolvedAt == nil {
		t.Fatalf("resolved evaluation = %#v", resolved)
	}
	if count := alertEventCount(t, handle, resolved.ID); count != 2 {
		t.Fatalf("resolved event count = %d, want 2", count)
	}

	lastSeen = time.Now().UTC().Add(-time.Hour)
	if err := handle.Model(&models.Node{}).Where("id = ?", nodeID).Update("last_seen", lastSeen).Error; err != nil {
		t.Fatal(err)
	}
	retriggered := findRuleAlert(t, engine.Evaluate(context.Background(), tenantID), RuleNodeOffline)
	if retriggered.State != StateFiring || retriggered.StartedAt == nil || retriggered.StartedAt.Equal(startedAt) {
		t.Fatalf("retrigger evaluation = %#v", retriggered)
	}
	if count := alertEventCount(t, handle, retriggered.ID); count != 3 {
		t.Fatalf("retrigger event count = %d, want 3", count)
	}
	if recorder.count() != 3 {
		t.Fatalf("notification count = %d, want 3 state-transition notifications", recorder.count())
	}
}

func TestWebhookRetriesAreBounded(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 3 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	notifier, err := NewWebhookNotifier(server.URL, time.Second, 3, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	notification := Notification{Event: "alert.firing", Schema: "neilico.alert.v1", Timestamp: time.Now().UTC()}
	if err := notifier.Notify(context.Background(), notification); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("webhook calls = %d, want 4 (initial + 3 retries)", got)
	}

	calls.Store(0)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "always fails", http.StatusInternalServerError)
	}))
	defer failing.Close()
	notifier, err = NewWebhookNotifier(failing.URL, time.Second, 3, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify(context.Background(), notification); err == nil {
		t.Fatal("expected webhook failure")
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("bounded failure calls = %d, want 4", got)
	}
}

func newAlertTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := handle.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return handle
}

func findRuleAlert(t *testing.T, values []Alert, rule string) Alert {
	t.Helper()
	for _, value := range values {
		if value.Rule == rule && value.DataStatus != DataStatusInsufficientData {
			return value
		}
	}
	t.Fatalf("rule %s missing from %#v", rule, values)
	return Alert{}
}

func alertEventCount(t *testing.T, handle *gorm.DB, alertID string) int64 {
	t.Helper()
	id, err := uuid.Parse(alertID)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := handle.Model(&models.AlertEvent{}).Where("alert_id = ?", id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type recordingNotifier struct {
	events []Notification
}

func (n *recordingNotifier) Name() string { return "recording" }

func (n *recordingNotifier) Notify(_ context.Context, notification Notification) error {
	n.events = append(n.events, notification)
	return nil
}

func (n *recordingNotifier) count() int { return len(n.events) }

func TestConfigDispatchFailureRuleIsImmediate(t *testing.T) {
	now := time.Now().UTC()
	got := dispatchFailureAlerts([]dispatchFailureObservation{
		{TargetType: "node", TargetID: "node-1", LastError: "delivery failed", Failures: 3},
		{TargetType: "node", TargetID: "healthy", LastError: "", Failures: 0},
	}, now)
	if len(got) != 1 || got[0].Rule != RuleConfigDispatchFailed || got[0].TargetID != "node-1" || got[0].Value != 3 {
		t.Fatalf("dispatch failure alerts = %#v", got)
	}
}

func TestEngineEvaluatesConfigDispatchFailure(t *testing.T) {
	handle := newAlertTestDB(t)
	tenantID := uuid.New()
	targetID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "dispatch-alert", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := handle.Create(&models.ConfigDispatchFailure{
		ID: uuid.New(), TenantID: tenantID, TargetType: "node", TargetID: targetID,
		LastError: "delivery failed", Failures: 2, FailedAt: time.Now().UTC(),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(handle, Options{}, nil, nil, testLogger())
	got := findRuleAlert(t, engine.Evaluate(context.Background(), tenantID), RuleConfigDispatchFailed)
	if got.TargetID != targetID.String() || got.Value != 2 || got.State != StateFiring {
		t.Fatalf("config dispatch evaluation = %#v", got)
	}
	if err := handle.Where("target_id = ?", targetID).Delete(&models.ConfigDispatchFailure{}).Error; err != nil {
		t.Fatal(err)
	}
	resolved := findRuleAlert(t, engine.Evaluate(context.Background(), tenantID), RuleConfigDispatchFailed)
	if resolved.State != StateResolved {
		t.Fatalf("config dispatch resolution = %#v", resolved)
	}
}
