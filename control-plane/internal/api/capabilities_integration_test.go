package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
)

type capabilitiesResponse struct {
	Mesh         string `json:"mesh"`
	SubnetRoutes string `json:"subnet_routes"`
	Tunnel       string `json:"tunnel"`
	Reason       string `json:"reason"`
}

type nodeCapabilitiesDetail struct {
	ID               uuid.UUID            `json:"id"`
	VirtualIP        string               `json:"virtual_ip"`
	NetworkID        *uuid.UUID           `json:"network_id"`
	Status           string               `json:"status"`
	LastSeen         *time.Time           `json:"last_seen"`
	HeartbeatStale   bool                 `json:"heartbeat_stale"`
	EffectiveMesh    string               `json:"effective_mesh"`
	CapabilitiesNote string               `json:"capabilities_note"`
	Capabilities     capabilitiesResponse `json:"capabilities"`
}

func TestUnavailableCapabilitiesDoNotBreakEnrollOrHeartbeat(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", admin.Token, map[string]any{
		"name": "capabilities", "cidr": "100.64.91.0/24",
	})
	requireStatus(t, status, http.StatusCreated)
	var network models.VirtualNetwork
	decodeResponse(t, body, &network)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", admin.Token, map[string]any{
		"network_id": network.ID, "expires_in_seconds": 3600, "max_uses": 1,
	})
	requireStatus(t, status, http.StatusCreated)
	var created enrollTokenCreateResponse
	decodeSecretResponse(t, body, &created)

	reported := map[string]any{
		"mesh": "unavailable", "subnet_routes": "unavailable", "tunnel": "unavailable",
		"reason": "缺 wg 工具 / 无 TUN / 需管理员权限",
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": created.Token, "name": "limited-node", "os": "windows", "arch": "amd64",
		"version": "test", "capabilities": reported,
	})
	requireStatus(t, status, http.StatusOK)
	var enrolled nodeEnrollResponse
	decodeResponse(t, body, &enrolled)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+enrolled.NodeID.String()+"/heartbeat", enrolled.AgentToken, map[string]any{
		"version": "test", "capabilities": reported,
	})
	requireStatus(t, status, http.StatusOK)

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+enrolled.NodeID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var detail nodeCapabilitiesDetail
	decodeResponse(t, body, &detail)
	if detail.Capabilities != (capabilitiesResponse{
		Mesh: "unavailable", SubnetRoutes: "unavailable", Tunnel: "unavailable",
		Reason: "缺 wg 工具 / 无 TUN / 需管理员权限",
	}) {
		t.Fatalf("stored capabilities = %#v", detail.Capabilities)
	}
	if detail.EffectiveMesh != "unavailable" {
		t.Fatalf("effective_mesh = %q, want unavailable", detail.EffectiveMesh)
	}
	if detail.VirtualIP != enrolled.VirtualIP || detail.NetworkID == nil || *detail.NetworkID != network.ID {
		t.Fatalf("node detail omitted membership: virtual_ip=%q network_id=%v", detail.VirtualIP, detail.NetworkID)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list struct {
		Items []nodeCapabilitiesDetail `json:"items"`
	}
	decodeResponse(t, body, &list)
	found := false
	for _, item := range list.Items {
		if item.ID == enrolled.NodeID {
			found = true
			if item.Capabilities != detail.Capabilities {
				t.Fatalf("node list capabilities = %#v, detail = %#v", item.Capabilities, detail.Capabilities)
			}
			if item.EffectiveMesh != "unavailable" {
				t.Fatalf("node list effective_mesh = %q", item.EffectiveMesh)
			}
		}
	}
	if !found {
		t.Fatalf("node list omitted %s: %s", enrolled.NodeID, body)
	}
}

// 节点 API 必须把「有效状态」以 tunnel 为准暴露出来：同一对象里 mesh=ready 与
// tunnel=unavailable 自相矛盾时，effective_mesh 必须更悲观，并给出说明；同时
// last_seen/heartbeat_stale 让「在线」有时效依据。
func TestNodeApiSurfacesEffectiveMeshAndHeartbeatStaleness(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", admin.Token, map[string]any{
		"name": "effective-mesh", "cidr": "100.64.92.0/24",
	})
	requireStatus(t, status, http.StatusCreated)
	var network models.VirtualNetwork
	decodeResponse(t, body, &network)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", admin.Token, map[string]any{
		"network_id": network.ID, "expires_in_seconds": 3600, "max_uses": 1,
	})
	requireStatus(t, status, http.StatusCreated)
	var created enrollTokenCreateResponse
	decodeSecretResponse(t, body, &created)

	// 自相矛盾：mesh=tunnel 自报利好，tunnel 却如实报不可用（本 bug 的现场取证）。
	contradictory := map[string]any{
		"mesh": "ready", "subnet_routes": "ready", "tunnel": "unavailable",
		"reason": "无法创建 WireGuard 接口：operation not permitted",
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": created.Token, "name": "contradictory-node", "os": "linux", "arch": "amd64",
		"version": "test", "capabilities": contradictory,
	})
	requireStatus(t, status, http.StatusOK)
	var enrolled nodeEnrollResponse
	decodeResponse(t, body, &enrolled)

	// 尚未心跳：last_seen 为空 → 陈旧。
	detail := fetchNodeDetail(t, app, admin.Token, enrolled.NodeID)
	if detail.EffectiveMesh != "unavailable" {
		t.Fatalf("effective_mesh = %q, want unavailable (tunnel wins over mesh=ready)", detail.EffectiveMesh)
	}
	if !strings.Contains(detail.CapabilitiesNote, "自相矛盾") {
		t.Fatalf("capabilities_note = %q, want contradiction note", detail.CapabilitiesNote)
	}
	if !detail.HeartbeatStale {
		t.Fatalf("node without heartbeat must be stale: last_seen=%v", detail.LastSeen)
	}

	// 心跳之后 last_seen 落地，时效性恢复。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+enrolled.NodeID.String()+"/heartbeat", enrolled.AgentToken, map[string]any{
		"version": "test", "capabilities": contradictory,
	})
	requireStatus(t, status, http.StatusOK)
	detail = fetchNodeDetail(t, app, admin.Token, enrolled.NodeID)
	if detail.HeartbeatStale {
		t.Fatalf("node with fresh heartbeat must not be stale: last_seen=%v", detail.LastSeen)
	}
	if detail.LastSeen == nil {
		t.Fatalf("last_seen must be exposed after a heartbeat")
	}
	if detail.EffectiveMesh != "unavailable" {
		t.Fatalf("effective_mesh = %q after heartbeat, want unavailable", detail.EffectiveMesh)
	}
}

func fetchNodeDetail(t *testing.T, app testApp, token string, nodeID uuid.UUID) nodeCapabilitiesDetail {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+nodeID.String(), token, nil)
	requireStatus(t, status, http.StatusOK)
	var detail nodeCapabilitiesDetail
	decodeResponse(t, body, &detail)
	return detail
}
