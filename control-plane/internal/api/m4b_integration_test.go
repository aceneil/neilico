package api_test

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

func TestAuditLogsTenantIsolationPaginationAndFilters(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	tenantA, userA := createM4BUser(t, app, admin.Token, "audit-a", "audit-a@example.test", auth.RoleOps)
	createM4BUser(t, app, admin.Token, "audit-b", "audit-b@example.test", auth.RoleTenantAdmin)
	_, readonly := createM4BUser(t, app, admin.Token, "audit-ro", "audit-ro@example.test", auth.RoleReadonly)

	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	for index := 0; index < 5; index++ {
		record := models.AuditLog{
			ID: uuid.New(), TenantID: &tenantA, UserID: &userA.User.ID,
			Action: "neilico.page", Resource: "/test/page", Detail: datatypes.JSON(`{"test":true}`),
			IP: "127.0.0.1", CreatedAt: base.Add(time.Duration(index) * time.Minute),
		}
		if err := app.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	otherTenantID := tenantIDForEmail(t, app, "audit-b@example.test")
	otherRecord := models.AuditLog{
		ID: uuid.New(), TenantID: &otherTenantID, Action: "neilico.page", Resource: "/test/page",
		Detail: datatypes.JSON(`{"other":true}`), IP: "127.0.0.1", CreatedAt: base.Add(time.Hour),
	}
	if err := app.db.Create(&otherRecord).Error; err != nil {
		t.Fatal(err)
	}

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/audit-logs?action=neilico.page&page=1&page_size=2&tenant_id="+otherTenantID.String(), userA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var pageOne service.AuditLogList
	decodeResponse(t, body, &pageOne)
	if pageOne.Total != 5 || pageOne.Page != 1 || pageOne.PageSize != 2 || len(pageOne.Items) != 2 {
		t.Fatalf("unexpected audit page one: %#v", pageOne)
	}
	if pageOne.Items[0].CreatedAt.Before(pageOne.Items[1].CreatedAt) {
		t.Fatalf("audit logs are not sorted descending: %#v", pageOne.Items)
	}
	for _, item := range pageOne.Items {
		if item.TenantID == nil || *item.TenantID != tenantA {
			t.Fatalf("cross-tenant audit item leaked: %#v", item)
		}
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/audit-logs?action=neilico.page&page=3&page_size=2", userA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var pageThree service.AuditLogList
	decodeResponse(t, body, &pageThree)
	if pageThree.Total != 5 || len(pageThree.Items) != 1 {
		t.Fatalf("unexpected audit page three: %#v", pageThree)
	}

	from := base.Add(2 * time.Minute).Format(time.RFC3339)
	to := base.Add(3*time.Minute + time.Second).Format(time.RFC3339)
	path := "/api/v1/audit-logs?action=neilico.page&from=" + url.QueryEscape(from) + "&to=" + url.QueryEscape(to) + "&page_size=200"
	status, body = mustRequest(t, app.server, http.MethodGet, path, userA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var filtered service.AuditLogList
	decodeResponse(t, body, &filtered)
	if filtered.Total != 2 || len(filtered.Items) != 2 {
		t.Fatalf("unexpected filtered audit logs: %#v", filtered)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/audit-logs?action=neilico.page&tenant_id="+tenantA.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var platformFiltered service.AuditLogList
	decodeResponse(t, body, &platformFiltered)
	if platformFiltered.Total != 5 {
		t.Fatalf("platform tenant filter total = %d, want 5: %s", platformFiltered.Total, body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/audit-logs?page_size=201", userA.Token, nil)
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_pagination")

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/audit-logs?page_size=200", readonly.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var readonlyList service.AuditLogList
	decodeResponse(t, body, &readonlyList)
	if readonlyList.Items == nil {
		t.Fatal("audit list items must be an array")
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/metrics", "", nil)
	requireStatus(t, status, http.StatusOK)
	if !bytes.Contains(body, []byte(`neilico_http_requests_total{method="GET",path="/api/v1/audit-logs"`)) {
		t.Fatalf("HTTP request metrics omitted audit endpoint:\n%s", body)
	}
}

func TestNodeMetricsTrafficAggregationAndEmptyArray(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	tenantA, userA := createM4BUser(t, app, admin.Token, "metrics-a", "metrics-a@example.test", auth.RoleOps)
	tenantB, _ := createM4BUser(t, app, admin.Token, "metrics-b", "metrics-b@example.test", auth.RoleTenantAdmin)
	otherAdmin := mustLogin(t, app, "metrics-b@example.test", "m4b-test-password")

	now := time.Now().UTC().Truncate(time.Second)
	node := models.Node{
		ID: uuid.New(), TenantID: tenantA, Name: "metrics-node", PublicKey: "metrics-public-key",
		OS: "linux", Arch: "amd64", Version: "1.0.0", Status: service.NodeStatusOnline,
		LastSeen: timePointer(now.Add(-10 * time.Second)), Tags: datatypes.JSONSlice[string]{},
		AgentTokenHash: strings.Repeat("a", 64), CreatedAt: now.Add(-3 * time.Hour),
	}
	if err := app.db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	trafficEntries := []models.TrafficLog{
		{ID: uuid.New(), TenantID: tenantA, NodeID: node.ID, Direction: "in", Bytes: 100, Protocol: "tcp", Peer: "198.51.100.1:443", CreatedAt: now.Add(-time.Hour)},
		{ID: uuid.New(), TenantID: tenantA, NodeID: node.ID, Direction: "out", Bytes: 250, Protocol: "tcp", Peer: "198.51.100.1:443", CreatedAt: now.Add(-time.Hour)},
		{ID: uuid.New(), TenantID: tenantA, NodeID: node.ID, Direction: "in", Bytes: 50, Protocol: "udp", Peer: "198.51.100.2:51820", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: uuid.New(), TenantID: tenantA, NodeID: node.ID, Direction: "out", Bytes: 75, Protocol: "udp", Peer: "198.51.100.2:51820", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: uuid.New(), TenantID: tenantA, NodeID: node.ID, Direction: "in", Bytes: 999, Protocol: "tcp", Peer: "198.51.100.3:443", CreatedAt: now.Add(-25 * time.Hour)},
	}
	if err := app.db.Create(&trafficEntries).Error; err != nil {
		t.Fatal(err)
	}

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+node.ID.String()+"/metrics?window_hours=24", userA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var metrics service.NodeMetrics
	decodeResponse(t, body, &metrics)
	if metrics.NodeID != node.ID || metrics.Status != service.NodeStatusOnline {
		t.Fatalf("unexpected node metrics identity: %#v", metrics)
	}
	if metrics.HeartbeatIntervalSeconds != 30 || metrics.Traffic.WindowHours != 24 {
		t.Fatalf("unexpected node metrics metadata: %#v", metrics)
	}
	if metrics.Traffic.InBytes != 150 || metrics.Traffic.OutBytes != 325 {
		t.Fatalf("24h traffic totals = %#v, want in=150 out=325", metrics.Traffic)
	}
	if len(metrics.RecentTraffic) != 2 {
		t.Fatalf("recent traffic points = %d, want 2: %#v", len(metrics.RecentTraffic), metrics.RecentTraffic)
	}
	var recentIn, recentOut int64
	for _, point := range metrics.RecentTraffic {
		recentIn += point.In
		recentOut += point.Out
	}
	if recentIn != metrics.Traffic.InBytes || recentOut != metrics.Traffic.OutBytes {
		t.Fatalf("recent traffic sums = %d/%d, totals = %#v", recentIn, recentOut, metrics.Traffic)
	}

	emptyNode := models.Node{
		ID: uuid.New(), TenantID: tenantA, Name: "metrics-empty", PublicKey: "metrics-empty-public-key",
		OS: "linux", Arch: "amd64", Version: "1.0.0", Status: service.NodeStatusOffline,
		Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: strings.Repeat("b", 64), CreatedAt: now.Add(-time.Minute),
	}
	if err := app.db.Create(&emptyNode).Error; err != nil {
		t.Fatal(err)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+emptyNode.ID.String()+"/metrics", userA.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var emptyMetrics service.NodeMetrics
	decodeResponse(t, body, &emptyMetrics)
	if emptyMetrics.Traffic.InBytes != 0 || emptyMetrics.Traffic.OutBytes != 0 {
		t.Fatalf("empty traffic totals = %#v", emptyMetrics.Traffic)
	}
	if emptyMetrics.RecentTraffic == nil || len(emptyMetrics.RecentTraffic) != 0 {
		t.Fatalf("empty recent traffic must be [] not null: %s", body)
	}

	crossNode := models.Node{
		ID: uuid.New(), TenantID: tenantB, Name: "metrics-other", PublicKey: "metrics-other-public-key",
		OS: "linux", Arch: "amd64", Version: "1.0.0", Status: service.NodeStatusOnline,
		Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: strings.Repeat("c", 64), CreatedAt: now,
	}
	if err := app.db.Create(&crossNode).Error; err != nil {
		t.Fatal(err)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+crossNode.ID.String()+"/metrics", userA.Token, nil)
	requireStatus(t, status, http.StatusNotFound)
	if bytes.Contains(body, []byte(crossNode.ID.String())) || bytes.Contains(body, []byte("metrics-other")) {
		t.Fatalf("cross-tenant node metrics leaked data: %s", body)
	}
	status, _ = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+crossNode.ID.String()+"/metrics", otherAdmin.Token, nil)
	requireStatus(t, status, http.StatusOK)

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+node.ID.String()+"/metrics?window_hours=169", userA.Token, nil)
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")
}

func TestRelayServerCRUDValidationAndPermissionMatrix(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	_, tenantAdmin := createM4BUser(t, app, admin.Token, "relay-admin", "relay-admin@example.test", auth.RoleTenantAdmin)
	_, ops := createM4BUser(t, app, admin.Token, "relay-ops", "relay-ops@example.test", auth.RoleOps)
	_, readonly := createM4BUser(t, app, admin.Token, "relay-readonly", "relay-readonly@example.test", auth.RoleReadonly)

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/relay-servers", readonly.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var empty service.RelayServerList
	decodeResponse(t, body, &empty)
	if empty.Total != 0 || empty.Items == nil {
		t.Fatalf("empty relay response must contain [] and zero total: %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/relay-servers", readonly.Token, map[string]any{
		"name": "readonly-write", "endpoint": "relay.example.test:51820", "region": "cn-test",
	})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/relay-servers", tenantAdmin.Token, map[string]any{
		"name": "relay-shanghai", "endpoint": "relay.example.test:51820", "region": "cn-shanghai",
	})
	requireStatus(t, status, http.StatusCreated)
	var relay service.RelayServerView
	decodeResponse(t, body, &relay)
	if relay.ID == uuid.Nil || relay.Status != "offline" || relay.LastSeen != nil {
		t.Fatalf("unexpected relay create response: %#v", relay)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/relay-servers", tenantAdmin.Token, map[string]any{
		"name": "relay-shanghai", "endpoint": "other.example.test:51820", "region": "cn-shanghai",
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "conflict")

	for _, endpoint := range []string{"relay.example.test", "https://relay.example.test:51820", "relay.example.test:0", "relay example:51820"} {
		status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/relay-servers", tenantAdmin.Token, map[string]any{
			"name": "invalid-" + uuid.NewString(), "endpoint": endpoint, "region": "cn-test",
		})
		requireStatus(t, status, http.StatusBadRequest)
		requireErrorCode(t, body, "invalid_request")
	}

	lastSeen := time.Now().UTC().Truncate(time.Second)
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/relay-servers/"+relay.ID.String(), tenantAdmin.Token, map[string]any{
		"name": "relay-shanghai-updated", "endpoint": "127.0.0.1:51820", "region": "cn-shanghai", "last_seen": lastSeen,
	})
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &relay)
	if relay.Status != "online" || relay.LastSeen == nil || !relay.LastSeen.Equal(lastSeen) {
		t.Fatalf("status was not derived from last_seen: %#v", relay)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/relay-servers", ops.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.RelayServerList
	decodeResponse(t, body, &list)
	if list.Total != 1 || list.Items[0].Status != "online" {
		t.Fatalf("unexpected relay list: %#v", list)
	}

	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/relay-servers/"+relay.ID.String(), readonly.Token, map[string]any{
		"name": "forbidden", "endpoint": "forbidden.example.test:1", "region": "cn-test",
	})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")
	status, body = mustRequest(t, app.server, http.MethodDelete, "/api/v1/relay-servers/"+relay.ID.String(), readonly.Token, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")

	status, _ = mustRequest(t, app.server, http.MethodDelete, "/api/v1/relay-servers/"+relay.ID.String(), tenantAdmin.Token, nil)
	requireStatus(t, status, http.StatusNoContent)
	status, body = mustRequest(t, app.server, http.MethodDelete, "/api/v1/relay-servers/"+relay.ID.String(), tenantAdmin.Token, nil)
	requireStatus(t, status, http.StatusNotFound)
	requireErrorCode(t, body, "not_found")

	_ = ops
}

func TestNetworkStatusUsesMemberOnlineState(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	tenantID, user := createM4BUser(t, app, admin.Token, "network-status", "network-status@example.test", auth.RoleOps)
	now := time.Now().UTC()
	online := models.Node{ID: uuid.New(), TenantID: tenantID, Name: "status-online", PublicKey: "online-key", OS: "linux", Arch: "amd64", Version: "1", Status: service.NodeStatusOnline, Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: strings.Repeat("d", 64), CreatedAt: now}
	offline := models.Node{ID: uuid.New(), TenantID: tenantID, Name: "status-offline", PublicKey: "offline-key", OS: "linux", Arch: "amd64", Version: "1", Status: service.NodeStatusOffline, Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: strings.Repeat("e", 64), CreatedAt: now}
	network := models.VirtualNetwork{ID: uuid.New(), TenantID: tenantID, Name: "status-network", CIDR: "100.64.50.0/24", CreatedAt: now}
	members := []models.NetworkMember{
		{ID: uuid.New(), NetworkID: network.ID, NodeID: online.ID, VirtualIP: "100.64.50.2", Role: "member", JoinedAt: now},
		{ID: uuid.New(), NetworkID: network.ID, NodeID: offline.ID, VirtualIP: "100.64.50.3", Role: "member", JoinedAt: now},
	}
	for _, value := range []any{&online, &offline, &network, &members} {
		if err := app.db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/networks/"+network.ID.String()+"/status", user.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var result service.NetworkStatus
	decodeResponse(t, body, &result)
	if result.NetworkID != network.ID || result.MemberCount != 2 || result.OnlineMemberCount != 1 {
		t.Fatalf("unexpected network status: %#v", result)
	}
	if result.Tunnels.Total != 2 || result.Tunnels.Up != 1 || result.Tunnels.Down != 1 || result.Tunnels.Basis != "member_status" {
		t.Fatalf("unexpected tunnel summary: %#v", result.Tunnels)
	}
}

func createM4BUser(t *testing.T, app testApp, adminToken, tenantName, email, role string) (uuid.UUID, loginResponse) {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", adminToken, map[string]any{"name": tenantName, "plan": "pro"})
	requireStatus(t, status, http.StatusCreated)
	var tenant models.Tenant
	decodeResponse(t, body, &tenant)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", adminToken, map[string]any{
		"tenant_id": tenant.ID, "email": email, "password": "m4b-test-password", "role": role, "status": "active",
	})
	requireStatus(t, status, http.StatusCreated)
	var user models.User
	decodeResponse(t, body, &user)
	return tenant.ID, mustLogin(t, app, email, "m4b-test-password")
}

func tenantIDForEmail(t *testing.T, app testApp, email string) uuid.UUID {
	t.Helper()
	var user models.User
	if err := app.db.First(&user, "email = ?", email).Error; err != nil {
		t.Fatal(err)
	}
	return user.TenantID
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func TestM4BRoutesExposeExpectedHTTPMethods(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/audit-logs", admin.Token, map[string]any{})
	requireStatus(t, status, http.StatusMethodNotAllowed)
	if !bytes.Contains(body, []byte("method not allowed")) {
		t.Fatalf("unexpected method response status=%d body=%s", status, body)
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/relay-servers/"+uuid.NewString(), admin.Token, nil)
	requireStatus(t, status, http.StatusMethodNotAllowed)
	_ = body
}
