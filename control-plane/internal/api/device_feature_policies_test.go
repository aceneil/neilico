package api_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

type featurePolicyEnvelope struct {
	Item service.DevicePolicyView `json:"item"`
}

type featurePolicyListEnvelope struct {
	Items []service.DevicePolicyView `json:"items"`
	Total int64                      `json:"total"`
}

type policyNodeRegistration struct {
	NodeID     uuid.UUID `json:"node_id"`
	AgentToken string    `json:"agent_token"`
}

func registerFeaturePolicyNode(t *testing.T, app testApp, token, name string) policyNodeRegistration {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", token, map[string]any{
		"name": name, "public_key": "k-" + name, "os": "linux", "arch": "amd64", "version": "0.1.0", "tags": []string{},
	})
	requireStatus(t, status, http.StatusCreated)
	var registered policyNodeRegistration
	decodeResponse(t, body, &registered)
	if registered.NodeID == uuid.Nil || registered.AgentToken == "" {
		t.Fatalf("registration omitted node identity/token: %s", body)
	}
	return registered
}

func patchFeaturePolicy(t *testing.T, app testApp, token string, nodeID uuid.UUID, features map[string]string) service.DevicePolicyView {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/device-policies/"+nodeID.String(), token,
		map[string]any{"features": features})
	requireStatus(t, status, http.StatusOK)
	if strings.Contains(string(body), `"item":null`) {
		t.Fatalf("PATCH returned a null item: %s", body)
	}
	var envelope featurePolicyEnvelope
	decodeResponse(t, body, &envelope)
	if envelope.Item.NodeID != nodeID {
		t.Fatalf("PATCH echo node_id = %s, want %s", envelope.Item.NodeID, nodeID)
	}
	return envelope.Item
}

func TestDeviceFeaturePoliciesThreeStatesEchoAndPersist(t *testing.T) {
	for _, feature := range []string{service.FeatureMesh, service.FeatureRemoteDesktop} {
		for _, state := range []service.FeatureState{service.FeatureAllow, service.FeatureDeny, service.FeatureForced} {
			t.Run(feature+"/"+string(state), func(t *testing.T) {
				app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
				admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
				node := registerFeaturePolicyNode(t, app, admin.Token, "tri-state")
				if feature == service.FeatureMesh {
					// forced 需要真实加入一个网络，证明它不是客户端侧常开标记。
					seedNetworkWithMember(t, app, admin.User.TenantID, node.NodeID, false)
				}

				echoed := patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{feature: string(state)})
				if echoed.Features[feature] != string(state) {
					t.Fatalf("PATCH echo %s = %q, want %q", feature, echoed.Features[feature], state)
				}
				if echoed.PolicyVersion != 1 || echoed.PolicyUpdatedAt.IsZero() || echoed.MinCacheTTLSeconds != 60 {
					t.Fatalf("policy metadata = version:%d updated:%s ttl:%d", echoed.PolicyVersion, echoed.PolicyUpdatedAt, echoed.MinCacheTTLSeconds)
				}

				status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/device-policies/"+node.NodeID.String(), admin.Token, nil)
				requireStatus(t, status, http.StatusOK)
				var item featurePolicyEnvelope
				decodeResponse(t, body, &item)
				if item.Item.Features[feature] != string(state) {
					t.Fatalf("GET %s = %q, want %q", feature, item.Item.Features[feature], state)
				}
				if !reflect.DeepEqual(echoed, item.Item) {
					t.Fatalf("PATCH echo differs from GET:\necho=%#v\nget=%#v", echoed, item.Item)
				}

				status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/device-policies", admin.Token, nil)
				requireStatus(t, status, http.StatusOK)
				var list featurePolicyListEnvelope
				decodeResponse(t, body, &list)
				if list.Total != 1 || list.Items[0].Features[feature] != string(state) {
					t.Fatalf("list did not persist %s=%s: %#v", feature, state, list)
				}
			})
		}
	}
}

func TestDeviceFeaturePolicyPatchRejectsUserToken(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	node := registerFeaturePolicyNode(t, app, admin.Token, "user-403")
	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": admin.User.TenantID, "email": "user@example.test", "password": "user-password",
		"role": auth.RoleReadonly, "status": "active",
	})
	requireStatus(t, status, http.StatusCreated)
	user := mustLogin(t, app, "user@example.test", "user-password")

	status, body := mustRequest(t, app.server, http.MethodPatch, "/api/v1/device-policies/"+node.NodeID.String(), user.Token,
		map[string]any{"features": map[string]string{service.FeatureRemoteDesktop: "allow"}})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/device-policies/"+node.NodeID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var item featurePolicyEnvelope
	decodeResponse(t, body, &item)
	if item.Item.Features[service.FeatureRemoteDesktop] != string(service.FeatureDeny) {
		t.Fatalf("user PATCH changed server policy: %#v", item.Item)
	}
}

func TestMeshForcedAfterDenyRejoins(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	node := registerFeaturePolicyNode(t, app, admin.Token, "mesh-deny-forced")
	seedNetworkWithMember(t, app, admin.User.TenantID, node.NodeID, true)

	patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureMesh: "deny"})
	forced := patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureMesh: "forced"})
	if !forced.Mesh.Joined {
		t.Fatalf("mesh forced after deny did not rejoin: %#v", forced.Mesh)
	}
	var members int64
	if err := app.db.Model(&models.NetworkMember{}).Where("node_id = ?", node.NodeID).Count(&members).Error; err != nil {
		t.Fatal(err)
	}
	if members != 1 {
		t.Fatalf("network members after forced = %d, want 1", members)
	}
}

func TestMeshDenyRemovesMemberAndRefusesWireGuardConfig(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	node := registerFeaturePolicyNode(t, app, admin.Token, "mesh-hard-block")
	seedNetworkWithMember(t, app, admin.User.TenantID, node.NodeID, true)

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/agent/config?node_id="+node.NodeID.String(), node.AgentToken, nil)
	requireStatus(t, status, http.StatusOK)
	if !strings.Contains(string(body), `"wireguard_config"`) {
		t.Fatalf("mesh allow response omitted WireGuard config: %s", body)
	}

	updated := patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureMesh: "deny"})
	if updated.Mesh.Joined {
		t.Fatalf("mesh deny echo still joined: %#v", updated.Mesh)
	}
	var members int64
	if err := app.db.Model(&models.NetworkMember{}).Where("node_id = ?", node.NodeID).Count(&members).Error; err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("network members after mesh deny = %d, want 0", members)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/agent/config?node_id="+node.NodeID.String(), node.AgentToken, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "policy_denied")
	if strings.Contains(string(body), `"wireguard_config":"`) || strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatalf("mesh deny response leaked WireGuard config: %s", body)
	}
}

func TestRemoteDesktopDenyRefusesServerCredentials(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	node := registerFeaturePolicyNode(t, app, admin.Token, "rd-hard-block")
	patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureRemoteDesktop: "allow"})

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config?node_id="+node.NodeID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if !strings.Contains(string(body), `"id_server":"192.0.2.1"`) || !strings.Contains(string(body), testRemoteDesktopPublicKey) {
		t.Fatalf("allow config did not contain server credentials: %s", body)
	}

	patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureRemoteDesktop: "deny"})
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/remote-desktop/config?node_id="+node.NodeID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "policy_denied")
	for _, secret := range []string{`"id_server"`, `"relay_server"`, `"public_key"`, testRemoteDesktopPublicKey} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("remote_desktop deny leaked %q: %s", secret, body)
		}
	}
}

func TestRemoteDesktopRuntimeRejectsStaleCacheAndNextHeartbeat(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	node := registerFeaturePolicyNode(t, app, admin.Token, "rd-runtime")
	policy := patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureRemoteDesktop: "allow"})

	registerBody := map[string]any{
		"node_id": node.NodeID, "policy_version": policy.PolicyVersion,
		"policy_fetched_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/register", node.AgentToken, registerBody)
	requireStatus(t, status, http.StatusOK)
	if !strings.Contains(string(body), `"active":true`) {
		t.Fatalf("register did not activate runtime: %s", body)
	}
	acceptedLogAt := time.Now().UTC()

	// 未重新拉取的旧版本在下一次注册/心跳前必须被拒绝。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/register", node.AgentToken, map[string]any{
		"node_id": node.NodeID, "policy_version": policy.PolicyVersion - 1,
		"policy_fetched_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "policy_stale")

	updated := patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureRemoteDesktop: "deny"})
	_ = updated
	// 已建连设备的下一轮心跳立即 403；旧版本当然也过不了新鲜度闸。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/remote-desktop/heartbeat", node.AgentToken, registerBody)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "policy_denied")
	rejectedLogAt := time.Now().UTC()

	logs := app.logs.String()
	acceptedMarker := `msg="remote desktop runtime accepted"`
	rejectedMarker := `msg="remote desktop runtime rejected by device policy"`
	if !strings.Contains(logs, acceptedMarker) || !strings.Contains(logs, rejectedMarker) {
		t.Fatalf("runtime log markers missing:\n%s", logs)
	}
	if !strings.Contains(logs, "reason=policy_denied") || !strings.Contains(logs, "policy_version=") {
		t.Fatalf("runtime reject log lacks reason/version:\n%s", logs)
	}
	if rejectedLogAt.Before(acceptedLogAt) {
		t.Fatalf("test timestamps moved backwards: accepted=%s rejected=%s", acceptedLogAt, rejectedLogAt)
	}
}

func TestDevicePolicyAuditContainsBeforeAndAfter(t *testing.T) {
	app := newRemoteDesktopApp(t, writePublicKey(t, testRemoteDesktopPublicKey))
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	node := registerFeaturePolicyNode(t, app, admin.Token, "policy-audit")
	patchFeaturePolicy(t, app, admin.Token, node.NodeID, map[string]string{service.FeatureMesh: "deny"})

	var audit models.AuditLog
	if err := app.db.Where("action = ?", "device_policy.update").Order("created_at DESC").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	detail := string(audit.Detail)
	for _, marker := range []string{`"before"`, `"after"`, `"from":"allow"`, `"to":"deny"`, `"policy_version"`} {
		if !strings.Contains(detail, marker) {
			t.Fatalf("audit detail missing %s: %s", marker, detail)
		}
	}
	if audit.UserID == nil || audit.CreatedAt.IsZero() {
		t.Fatalf("audit lacks actor/time: %#v", audit)
	}
}
