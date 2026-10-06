package api_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

// proxyHostFixture 生成一个租户管理员及其 token，供代理主机测试复用。
func proxyHostFixture(t *testing.T, app testApp) (token string, tenantID uuid.UUID) {
	t.Helper()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "proxy-hosts", "plan": "pro"})
	requireStatus(t, status, http.StatusCreated)
	var tenant models.Tenant
	decodeResponse(t, body, &tenant)
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": tenant.ID,
		"email":     "admin@proxy-hosts.test",
		"password":  "tenant-password",
		"role":      auth.RoleTenantAdmin,
		"status":    "active",
	})
	requireStatus(t, status, http.StatusCreated)
	tenantAdmin := mustLogin(t, app, "admin@proxy-hosts.test", "tenant-password")
	return tenantAdmin.Token, tenant.ID
}

func emptyAccessControl() map[string]any {
	return map[string]any{
		"ip_whitelist": []string{},
		"basic_auth":   map[string]any{"enabled": false, "username": "", "password_hash": ""},
		"require_jwt":  false,
	}
}

// TestProxyHostsSingleStepFlow 覆盖 NPM 风格单步代理主机的核心保障：
// 一次提交同时生成 domain+rule、目标非法不落盘、单行编辑、删除级联、跨租户隔离，以及旧流程兼容。
func TestProxyHostsSingleStepFlow(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	token, tenantID := proxyHostFixture(t, app)

	// 注册一个节点，用于验证 node 目标类型。
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", token, map[string]any{
		"name":       "host-node",
		"public_key": "host-node-public-key",
		"os":         "linux",
		"arch":       "amd64",
		"version":    "test",
	})
	requireStatus(t, status, http.StatusCreated)
	var registered struct {
		NodeID uuid.UUID `json:"node_id"`
	}
	decodeResponse(t, body, &registered)

	// —— 单步创建：一次提交同时生成 domain 与默认 rule ——
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-hosts", token, map[string]any{
		"domain":          "app.example.com",
		"path":            "/",
		"target_type":     "internal_ip",
		"target":          "10.0.0.5:8080",
		"upstream_scheme": "http",
		"access_control":  emptyAccessControl(),
		"enabled":         true,
	})
	requireStatus(t, status, http.StatusCreated)
	var host service.ProxyHost
	decodeResponse(t, body, &host)
	if host.DomainID != host.ID || host.Domain != "app.example.com" || host.RuleID == nil {
		t.Fatalf("unexpected proxy host response: %#v", host)
	}
	if host.TargetType != "internal_ip" || host.Target != "10.0.0.5:8080" || host.RuleCount != 1 {
		t.Fatalf("proxy host target = %#v", host)
	}
	var storedDomain models.Domain
	if err := app.db.First(&storedDomain, "id = ?", host.DomainID).Error; err != nil {
		t.Fatalf("domain was not persisted: %v", err)
	}
	var storedRule models.ProxyRule
	if err := app.db.First(&storedRule, "id = ?", *host.RuleID).Error; err != nil {
		t.Fatalf("rule was not persisted in the same step: %v", err)
	}
	if storedRule.DomainID != host.DomainID || storedRule.Target != "10.0.0.5:8080" || storedRule.TenantID != tenantID {
		t.Fatalf("stored rule = %#v", storedRule)
	}

	// —— 目标非法：整笔回滚，不产生半成品 ——
	var domainCount int64
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-hosts", token, map[string]any{
		"domain":         "bad.example.com",
		"target_type":    "internal_ip",
		"target":         "not-an-ip:80",
		"access_control": emptyAccessControl(),
	})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")
	if err := app.db.Model(&models.Domain{}).Where("domain = ?", "bad.example.com").Count(&domainCount).Error; err != nil {
		t.Fatal(err)
	}
	if domainCount != 0 {
		t.Fatalf("invalid target left %d domains behind, want 0", domainCount)
	}
	// 非法 target_type 同样整笔回滚。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-hosts", token, map[string]any{
		"domain":         "bad-type.example.com",
		"target_type":    "bogus",
		"target":         "10.0.0.6:80",
		"access_control": emptyAccessControl(),
	})
	requireStatus(t, status, http.StatusBadRequest)
	if err := app.db.Model(&models.Domain{}).Where("domain = ?", "bad-type.example.com").Count(&domainCount).Error; err != nil {
		t.Fatal(err)
	}
	if domainCount != 0 {
		t.Fatalf("invalid target_type left %d domains behind, want 0", domainCount)
	}

	// —— 域名冲突：409 且不新增规则 ——
	var ruleCount int64
	if err := app.db.Model(&models.ProxyRule{}).Count(&ruleCount).Error; err != nil {
		t.Fatal(err)
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-hosts", token, map[string]any{
		"domain":         "app.example.com",
		"target_type":    "internal_ip",
		"target":         "10.0.0.9:80",
		"access_control": emptyAccessControl(),
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "conflict")
	var afterConflict int64
	if err := app.db.Model(&models.ProxyRule{}).Count(&afterConflict).Error; err != nil {
		t.Fatal(err)
	}
	if afterConflict != ruleCount {
		t.Fatalf("conflicting host created a stray rule: %d -> %d", ruleCount, afterConflict)
	}

	// —— 另外两种目标类型：virtual_ip 与 node ——
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-hosts", token, map[string]any{
		"domain":         "mesh.example.com",
		"target_type":    "virtual_ip",
		"target":         "100.64.0.2:9100",
		"access_control": emptyAccessControl(),
	})
	requireStatus(t, status, http.StatusCreated)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-hosts", token, map[string]any{
		"domain":         "node.example.com",
		"target_type":    "node",
		"target":         registered.NodeID.String() + ":9200",
		"access_control": emptyAccessControl(),
	})
	requireStatus(t, status, http.StatusCreated)
	var nodeHost service.ProxyHost
	decodeResponse(t, body, &nodeHost)
	if nodeHost.TargetType != "node" || nodeHost.Target != registered.NodeID.String()+":9200" {
		t.Fatalf("node host = %#v", nodeHost)
	}

	// —— 列表：一条主机一行，join 出域名 + 转发目标 ——
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/proxy-hosts?page_size=100", token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.ProxyHostList
	decodeResponse(t, body, &list)
	if list.Total != 3 || len(list.Items) != 3 {
		t.Fatalf("proxy host list total = %d items = %d, want 3", list.Total, len(list.Items))
	}
	found := false
	for _, item := range list.Items {
		if item.Domain == "app.example.com" {
			found = true
			if item.RuleID == nil || item.Target != "10.0.0.5:8080" {
				t.Fatalf("list row missing joined rule: %#v", item)
			}
		}
	}
	if !found {
		t.Fatal("app.example.com missing from proxy host list")
	}

	// —— 单行编辑：同一张表单同时改域名与转发目标 ——
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/proxy-hosts/"+host.ID.String(), token, map[string]any{
		"domain":          "app.example.com",
		"path":            "/",
		"target_type":     "internal_ip",
		"target":          "10.0.0.7:9090",
		"upstream_scheme": "https",
		"access_control":  emptyAccessControl(),
		"enabled":         false,
	})
	requireStatus(t, status, http.StatusOK)
	var updated service.ProxyHost
	decodeResponse(t, body, &updated)
	if updated.RuleID == nil || *updated.RuleID != *host.RuleID {
		t.Fatalf("edit changed the rule identity unexpectedly: %#v", updated)
	}
	if updated.Target != "10.0.0.7:9090" || updated.UpstreamScheme != "https" || updated.Enabled {
		t.Fatalf("edit did not apply: %#v", updated)
	}
	if err := app.db.First(&storedRule, "id = ?", *host.RuleID).Error; err != nil {
		t.Fatal(err)
	}
	if storedRule.Target != "10.0.0.7:9090" || storedRule.UpstreamScheme != "https" || storedRule.Enabled {
		t.Fatalf("stored rule after edit = %#v", storedRule)
	}
	// 编辑时域名本身也可以改。
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/proxy-hosts/"+host.ID.String(), token, map[string]any{
		"domain":          "app-renamed.example.com",
		"path":            "/",
		"target_type":     "internal_ip",
		"target":          "10.0.0.7:9090",
		"upstream_scheme": "https",
		"access_control":  emptyAccessControl(),
		"enabled":         true,
	})
	requireStatus(t, status, http.StatusOK)
	if err := app.db.First(&storedDomain, "id = ?", host.DomainID).Error; err != nil {
		t.Fatal(err)
	}
	if storedDomain.Domain != "app-renamed.example.com" {
		t.Fatalf("domain rename did not apply: %#v", storedDomain)
	}

	// —— 删除级联：域名与其全部规则同时清掉，不残留、不 500 ——
	status, body = mustRequest(t, app.server, http.MethodDelete, "/api/v1/proxy-hosts/"+host.ID.String(), token, nil)
	requireStatus(t, status, http.StatusNoContent)
	var cascadedDomains, cascadedRules int64
	if err := app.db.Model(&models.Domain{}).Where("id = ?", host.DomainID).Count(&cascadedDomains).Error; err != nil {
		t.Fatal(err)
	}
	if err := app.db.Model(&models.ProxyRule{}).Where("domain_id = ?", host.DomainID).Count(&cascadedRules).Error; err != nil {
		t.Fatal(err)
	}
	if cascadedDomains != 0 || cascadedRules != 0 {
		t.Fatalf("cascade delete left domains=%d rules=%d behind", cascadedDomains, cascadedRules)
	}

	// —— 跨租户隔离 ——
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "proxy-hosts-other", "plan": "free"})
	requireStatus(t, status, http.StatusCreated)
	var otherTenant models.Tenant
	decodeResponse(t, body, &otherTenant)
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID,
		"email":     "admin@proxy-hosts-other.test",
		"password":  "other-password",
		"role":      auth.RoleTenantAdmin,
	})
	requireStatus(t, status, http.StatusCreated)
	otherAdmin := mustLogin(t, app, "admin@proxy-hosts-other.test", "other-password")
	for _, probe := range []struct {
		method string
		target string
	}{
		{http.MethodGet, nodeHost.ID.String()},
		{http.MethodPut, nodeHost.ID.String()},
		{http.MethodDelete, nodeHost.ID.String()},
	} {
		if probe.method == http.MethodPut {
			status, body = mustRequest(t, app.server, probe.method, "/api/v1/proxy-hosts/"+probe.target, otherAdmin.Token, map[string]any{
				"domain":         "stolen.example.com",
				"target_type":    "internal_ip",
				"target":         "10.0.0.1:80",
				"access_control": emptyAccessControl(),
			})
		} else {
			status, body = mustRequest(t, app.server, probe.method, "/api/v1/proxy-hosts/"+probe.target, otherAdmin.Token, nil)
		}
		if status != http.StatusNotFound && status != http.StatusForbidden {
			t.Fatalf("cross-tenant %s status = %d body = %s", probe.method, status, body)
		}
	}
	// 其它租户的列表看不到本租户主机。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/proxy-hosts?page_size=100", otherAdmin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &list)
	if list.Total != 0 {
		t.Fatalf("cross-tenant list leaked %d hosts", list.Total)
	}
}

// TestProxyHostsBackwardCompatibleAndAdoptsLegacyDomain 验证：
// 旧 /domains + /proxy-rules 流程仍可用，且其数据会以「未绑定/已绑定」形式出现在代理主机列表；
// 对旧流程建出的、尚无规则的域名执行单行编辑时，补建默认规则而不是报错。
func TestProxyHostsBackwardCompatibleAndAdoptsLegacyDomain(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	token, _ := proxyHostFixture(t, app)

	// 旧流程：先建域名，再建规则。
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/domains", token, map[string]any{
		"domain": "legacy.example.com",
		"status": service.DomainStatusActive,
	})
	requireStatus(t, status, http.StatusCreated)
	var domain models.Domain
	decodeResponse(t, body, &domain)

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/proxy-hosts?page_size=100", token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.ProxyHostList
	decodeResponse(t, body, &list)
	if list.Total != 1 || list.Items[0].RuleID != nil || list.Items[0].RuleCount != 0 {
		t.Fatalf("legacy domain row = %#v", list.Items)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-rules", token, map[string]any{
		"domain_id":      domain.ID,
		"path":           "/",
		"target_type":    "internal_ip",
		"target":         "10.0.0.8:81",
		"access_control": emptyAccessControl(),
		"enabled":        true,
	})
	requireStatus(t, status, http.StatusCreated)

	// 无规则的旧域名被单行编辑时，应补建默认规则。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/domains", token, map[string]any{
		"domain": "adopt.example.com",
		"status": service.DomainStatusActive,
	})
	requireStatus(t, status, http.StatusCreated)
	var adopt models.Domain
	decodeResponse(t, body, &adopt)
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/proxy-hosts/"+adopt.ID.String(), token, map[string]any{
		"domain":         "adopt.example.com",
		"target_type":    "internal_ip",
		"target":         "10.0.0.10:82",
		"access_control": emptyAccessControl(),
	})
	requireStatus(t, status, http.StatusOK)
	var adopted service.ProxyHost
	decodeResponse(t, body, &adopted)
	if adopted.RuleID == nil || adopted.Target != "10.0.0.10:82" || adopted.RuleCount != 1 {
		t.Fatalf("legacy domain was not adopted with a default rule: %#v", adopted)
	}

	// 旧端点仍然可用：直接读写 /proxy-rules 与 /domains 均成功。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/proxy-rules?page_size=100", token, nil)
	requireStatus(t, status, http.StatusOK)
	var rules service.ProxyRuleList
	decodeResponse(t, body, &rules)
	if rules.Total != 2 {
		t.Fatalf("proxy-rules total = %d, want 2", rules.Total)
	}
	status, _ = mustRequest(t, app.server, http.MethodGet, "/api/v1/domains?page_size=100", token, nil)
	requireStatus(t, status, http.StatusOK)
}
