package api_test

import (
	"net/http"
	"testing"

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
	ID           uuid.UUID            `json:"id"`
	VirtualIP    string               `json:"virtual_ip"`
	NetworkID    *uuid.UUID           `json:"network_id"`
	Capabilities capabilitiesResponse `json:"capabilities"`
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
		}
	}
	if !found {
		t.Fatalf("node list omitted %s: %s", enrolled.NodeID, body)
	}
}
