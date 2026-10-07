package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

type policyEnvelope struct {
	Item service.RemoteDesktopDevicePolicyView `json:"item"`
}

// registerPolicyNode 通过正式接口注册一台节点，返回其 node_id。
func registerPolicyNode(t *testing.T, app testApp, token, name string) uuid.UUID {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", token, map[string]any{
		"name": name, "public_key": "k-" + name, "os": "linux", "arch": "amd64", "version": "0.1.0", "tags": []string{},
	})
	requireStatus(t, status, http.StatusCreated)
	var registered struct {
		NodeID   uuid.UUID `json:"node_id"`
		TenantID uuid.UUID `json:"tenant_id"`
	}
	decodeResponse(t, body, &registered)
	if registered.NodeID == uuid.Nil {
		t.Fatalf("node registration returned no node_id: %s", body)
	}
	return registered.NodeID
}

// seedNetworkWithMember 直接落库一个虚拟网络，并可选把节点加入（模拟现有成员能力已生效）。
func seedNetworkWithMember(t *testing.T, app testApp, tenantID, nodeID uuid.UUID, join bool) models.VirtualNetwork {
	t.Helper()
	network := models.VirtualNetwork{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "lan-" + uuid.NewString()[:8],
		CIDR:         "10.42.0.0/24",
		Secret:       "seed",
		PresharedKey: "seed",
		CreatedAt:    time.Now().UTC(),
	}
	if err := app.db.Create(&network).Error; err != nil {
		t.Fatalf("seed network: %v", err)
	}
	if join {
		member := models.NetworkMember{
			ID: uuid.New(), NetworkID: network.ID, NodeID: nodeID,
			VirtualIP: "10.42.0.7", Role: "member", JoinedAt: time.Now().UTC(),
		}
		if err := app.db.Create(&member).Error; err != nil {
			t.Fatalf("seed network member: %v", err)
		}
	}
	return network
}

func TestRemoteDesktopDevicePoliciesRequireAuth(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/device-policies", "", nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "unauthorized")

	status, _ = mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+uuid.NewString(), "",
		map[string]any{"remote_control_allowed": true})
	requireStatus(t, status, http.StatusUnauthorized)
}

// 面向公网的产品：未建过策略的节点必须默认「拒绝被远程」，枚举默认 auto。
func TestRemoteDesktopDevicePoliciesDefaultDeny(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-default")

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/device-policies", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.RemoteDesktopDevicePolicyList
	decodeResponse(t, body, &list)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("policy list = %#v, want one item", list)
	}
	item := list.Items[0]
	if item.NodeID != nodeID {
		t.Fatalf("node_id = %s, want %s", item.NodeID, nodeID)
	}
	if item.RemoteControlAllowed {
		t.Fatalf("remote_control_allowed = true, want default false (opt-in)")
	}
	if item.TunnelMode != service.TunnelModeAuto {
		t.Fatalf("tunnel_mode = %q, want auto", item.TunnelMode)
	}
	if item.IsolatedTunnel.Enabled || item.IsolatedTunnel.StreamRuleID != nil {
		t.Fatalf("isolated_tunnel = %#v, want disabled with no rule", item.IsolatedTunnel)
	}
	if item.Mesh.Joined || item.Mesh.NetworkID != nil {
		t.Fatalf("mesh = %#v, want not joined", item.Mesh)
	}
	if item.Readonly.SubnetRoutes != service.SubnetRoutesUnavailable {
		t.Fatalf("subnet_routes = %q, want unavailable", item.Readonly.SubnetRoutes)
	}
}

func TestRemoteDesktopDevicePolicyPatchRequiresAdmin(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-403")

	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": admin.User.TenantID, "email": "ops@example.test", "password": "ops-password",
		"role": auth.RoleOps, "status": "active",
	})
	requireStatus(t, status, http.StatusCreated)
	ops := mustLogin(t, app, "ops@example.test", "ops-password")

	// 非 admin（即使是有写权限的 ops）→ 403。
	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), ops.Token,
		map[string]any{"remote_control_allowed": true})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")

	// 确认没被改动。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/device-policies", ops.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.RemoteDesktopDevicePolicyList
	decodeResponse(t, body, &list)
	if list.Items[0].RemoteControlAllowed {
		t.Fatalf("non-admin PATCH changed the policy: %#v", list.Items[0])
	}
}

func TestRemoteDesktopDevicePolicyPatchPartialUpdateAndValidation(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-partial")

	// 只改 remote_control_allowed，其它字段保持默认。
	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"remote_control_allowed": true})
	requireStatus(t, status, http.StatusOK)
	var env policyEnvelope
	decodeResponse(t, body, &env)
	if !env.Item.RemoteControlAllowed || env.Item.TunnelMode != service.TunnelModeAuto {
		t.Fatalf("unexpected item after partial patch: %#v", env.Item)
	}

	// 再只改 tunnel_mode；remote_control_allowed 不应被重置。
	status, body = mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"tunnel_mode": "direct"})
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &env)
	if env.Item.TunnelMode != service.TunnelModeDirect || !env.Item.RemoteControlAllowed {
		t.Fatalf("partial update lost prior field: %#v", env.Item)
	}

	// 持久化：GET 反映改动，且确实建了策略行。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/device-policies", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list service.RemoteDesktopDevicePolicyList
	decodeResponse(t, body, &list)
	if list.Items[0].TunnelMode != service.TunnelModeDirect || !list.Items[0].RemoteControlAllowed {
		t.Fatalf("policy did not persist: %#v", list.Items[0])
	}
	var stored int64
	if err := app.db.Model(&models.RemoteDesktopDevicePolicy{}).Where("node_id = ?", nodeID).Count(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("stored policy rows = %d, want 1", stored)
	}

	// 非法枚举 → 400，且不改动已有值。
	status, body = mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"tunnel_mode": "bogus"})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")
}

func TestRemoteDesktopDevicePolicyPatchUnknownNode(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, _ := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+uuid.NewString(), admin.Token,
		map[string]any{"remote_control_allowed": true})
	requireStatus(t, status, http.StatusNotFound)

	status, _ = mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/not-a-uuid", admin.Token,
		map[string]any{"remote_control_allowed": true})
	requireStatus(t, status, http.StatusBadRequest)
}

// 单独隧道必须复用现有 StreamRule：开启建规则、关闭删规则。
func TestRemoteDesktopDevicePolicyIsolatedTunnelReusesStreamRule(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-tunnel")
	seedNetworkWithMember(t, app, admin.User.TenantID, nodeID, true)

	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"isolated_tunnel_enabled": true})
	requireStatus(t, status, http.StatusOK)
	var env policyEnvelope
	decodeResponse(t, body, &env)
	if !env.Item.IsolatedTunnel.Enabled || env.Item.IsolatedTunnel.StreamRuleID == nil {
		t.Fatalf("isolated tunnel not enabled: %#v", env.Item.IsolatedTunnel)
	}
	ruleID := *env.Item.IsolatedTunnel.StreamRuleID

	var rule models.StreamRule
	if err := app.db.First(&rule, "id = ?", ruleID).Error; err != nil {
		t.Fatalf("isolated stream rule was not persisted: %v", err)
	}
	if rule.Protocol != "tcp" || rule.TargetType != "node" || !strings.HasPrefix(rule.Target, nodeID.String()+":") {
		t.Fatalf("unexpected isolated stream rule: %#v", rule)
	}

	// 关闭 → 规则被删除，id 置空。
	status, body = mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"isolated_tunnel_enabled": false})
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &env)
	if env.Item.IsolatedTunnel.Enabled || env.Item.IsolatedTunnel.StreamRuleID != nil {
		t.Fatalf("isolated tunnel not disabled: %#v", env.Item.IsolatedTunnel)
	}
	var remaining int64
	if err := app.db.Model(&models.StreamRule{}).Where("id = ?", ruleID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("isolated stream rule %s was not deleted", ruleID)
	}
}

func TestRemoteDesktopDevicePolicyIsolatedTunnelNeedsVirtualIP(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-no-ip")

	// 节点未加入任何虚拟网络 → 没有转发目标，拒绝开启。
	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"isolated_tunnel_enabled": true})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")
}

// Mesh 加入/退出必须复用现有成员能力。
func TestRemoteDesktopDevicePolicyMeshJoinLeave(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-mesh")
	network := seedNetworkWithMember(t, app, admin.User.TenantID, nodeID, false)

	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"mesh_joined": true})
	requireStatus(t, status, http.StatusOK)
	var env policyEnvelope
	decodeResponse(t, body, &env)
	if !env.Item.Mesh.Joined || env.Item.Mesh.NetworkID == nil || *env.Item.Mesh.NetworkID != network.ID {
		t.Fatalf("mesh join did not take effect: %#v", env.Item.Mesh)
	}
	if env.Item.Mesh.VirtualIP == nil || strings.TrimSpace(*env.Item.Mesh.VirtualIP) == "" {
		t.Fatalf("mesh join produced no virtual IP: %#v", env.Item.Mesh)
	}
	var members int64
	if err := app.db.Model(&models.NetworkMember{}).Where("node_id = ?", nodeID).Count(&members).Error; err != nil {
		t.Fatal(err)
	}
	if members != 1 {
		t.Fatalf("network members = %d, want 1", members)
	}

	// 退出 → 成员关系移除。
	status, body = mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"mesh_joined": false})
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &env)
	if env.Item.Mesh.Joined || env.Item.Mesh.NetworkID != nil {
		t.Fatalf("mesh leave did not take effect: %#v", env.Item.Mesh)
	}
	if err := app.db.Model(&models.NetworkMember{}).Where("node_id = ?", nodeID).Count(&members).Error; err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("network members = %d after leave, want 0", members)
	}
}

func TestRemoteDesktopDevicePolicyMeshJoinWithoutNetwork(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-no-net")

	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"mesh_joined": true})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")
}

func TestRemoteDesktopDevicePolicyPatchIsAudited(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	nodeID := registerPolicyNode(t, app, admin.Token, "nas-audit")

	status, _ := mustRequest(t, app.server, http.MethodPatch, "/api/v1/remote-desktop/device-policies/"+nodeID.String(), admin.Token,
		map[string]any{"remote_control_allowed": true})
	requireStatus(t, status, http.StatusOK)

	var audits int64
	if err := app.db.Model(&models.AuditLog{}).Where("action = ?", "remote_desktop.policy.update").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits < 1 {
		t.Fatalf("policy update audit records = %d, want at least 1", audits)
	}
}
