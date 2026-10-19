package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

type subnetRouteView struct {
	ID       string `json:"id"`
	NodeID   string `json:"node_id"`
	CIDR     string `json:"cidr"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	Active   bool   `json:"active"`
}

type subnetRouteList struct {
	Items []subnetRouteView `json:"items"`
	Total int               `json:"total"`
}

func listSubnetRoutes(t *testing.T, app testApp, token string, networkID uuid.UUID) subnetRouteList {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/networks/"+networkID.String()+"/routes", token, nil)
	requireStatus(t, status, http.StatusOK)
	var list subnetRouteList
	decodeResponse(t, body, &list)
	return list
}

// 同一实体局域网的多台节点宣告同一条 CIDR：优先级决定主用，主节点掉线后备用接管。
func TestSubnetRouteRedundancyAndFailover(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", admin.Token, map[string]any{
		"name": "site-a", "cidr": "100.64.40.0/24",
	})
	requireStatus(t, status, http.StatusCreated)
	var network m2bNetworkCreate
	decodeResponse(t, body, &network)

	primary := registerM2BNode(t, app, admin.Token, "lan-primary")
	backup := registerM2BNode(t, app, admin.Token, "lan-backup")
	addM2BMember(t, app, admin.Token, network.ID, primary.NodeID)
	addM2BMember(t, app, admin.Token, network.ID, backup.NodeID)

	for _, node := range []m2bNodeRegistration{primary, backup} {
		status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+node.NodeID.String()+"/heartbeat", node.AgentToken, map[string]any{"version": "test"})
		requireStatus(t, status, http.StatusOK)
	}
	// 两台机器在同一个局域网：默认网关 MAC 相同（同一个路由器）→ 站点自动识别为同一组
	if err := app.db.Model(&models.Node{}).
		Where("id IN ?", []uuid.UUID{primary.NodeID, backup.NodeID}).
		Updates(map[string]any{"environment": "host", "gateway_on_physical": true, "gateway_mac": "aa:bb:cc:dd:ee:01", "gateway_ip": "192.168.1.1"}).Error; err != nil {
		t.Fatalf("seed gateway facts: %v", err)
	}

	// 两条同 CIDR 宣告：主 10、备 20
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": primary.NodeID, "cidr": "192.168.50.0/24", "enabled": true, "priority": 10,
	})
	requireStatus(t, status, http.StatusCreated)
	var primaryRoute subnetRouteView
	decodeResponse(t, body, &primaryRoute)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": backup.NodeID, "cidr": "192.168.50.0/24", "enabled": true, "priority": 20,
	})
	requireStatus(t, status, http.StatusCreated)
	var backupRoute subnetRouteView
	decodeResponse(t, body, &backupRoute)

	// 同一节点重复宣告同一 CIDR → 409 route_conflict
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": primary.NodeID, "cidr": "192.168.50.0/24", "enabled": true,
	})
	requireStatus(t, status, http.StatusConflict)
	if !strings.Contains(string(body), "route_conflict") {
		t.Fatalf("duplicate route should report route_conflict, got %s", body)
	}

	// 重叠网段 → 409 route_conflict
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": backup.NodeID, "cidr": "192.168.0.0/16", "enabled": true,
	})
	requireStatus(t, status, http.StatusConflict)

	// 两台都在线：只有主用那条 active
	list := listSubnetRoutes(t, app, admin.Token, network.ID)
	activeByID := map[string]bool{}
	for _, item := range list.Items {
		activeByID[item.ID] = item.Active
	}
	if !activeByID[primaryRoute.ID] || activeByID[backupRoute.ID] {
		t.Fatalf("expected primary active / backup standby, got %#v", list.Items)
	}

	// 主节点掉线：备用接管
	if err := app.db.Model(&models.Node{}).Where("id = ?", primary.NodeID).Update("status", service.NodeStatusOffline).Error; err != nil {
		t.Fatalf("mark primary offline: %v", err)
	}
	list = listSubnetRoutes(t, app, admin.Token, network.ID)
	activeByID = map[string]bool{}
	for _, item := range list.Items {
		activeByID[item.ID] = item.Active
	}
	if activeByID[primaryRoute.ID] || !activeByID[backupRoute.ID] {
		t.Fatalf("expected backup to take over, got %#v", list.Items)
	}

	// 触发一次配置重算（真实系统由 reconcile 循环 bump），确认 AllowedIPs 指向备用节点
	status, _ = mustRequest(t, app.server, http.MethodPut, "/api/v1/networks/"+network.ID.String()+"/routes/"+backupRoute.ID, admin.Token, map[string]any{
		"node_id": backup.NodeID, "cidr": "192.168.50.0/24", "enabled": true, "priority": 20,
	})
	requireStatus(t, status, http.StatusOK)

	config := getM2BConfig(t, app, primary.AgentToken, primary.NodeID.String(), 0)
	var backupPeer m2bPeer
	for _, peer := range config.Network.Peers {
		if peer.NodeID == backup.NodeID.String() {
			backupPeer = peer
		}
	}
	if !containsString(backupPeer.AllowedIPs, "192.168.50.0/24") {
		t.Fatalf("backup peer AllowedIPs = %#v, want the taken-over subnet", backupPeer.AllowedIPs)
	}

	// 优先级可调：把备用提到 5，回收后它应重新成为主用
	status, _ = mustRequest(t, app.server, http.MethodPut, "/api/v1/networks/"+network.ID.String()+"/routes/"+primaryRoute.ID, admin.Token, map[string]any{
		"node_id": primary.NodeID, "cidr": "192.168.50.0/24", "enabled": true, "priority": 50,
	})
	requireStatus(t, status, http.StatusOK)
	if err := app.db.Model(&models.Node{}).Where("id = ?", primary.NodeID).Update("status", service.NodeStatusOnline).Error; err != nil {
		t.Fatalf("mark primary online: %v", err)
	}
	list = listSubnetRoutes(t, app, admin.Token, network.ID)
	for _, item := range list.Items {
		if item.ID == primaryRoute.ID && item.Active {
			t.Fatalf("primary with priority 50 must stay standby: %#v", list.Items)
		}
		if item.ID == backupRoute.ID && !item.Active {
			t.Fatalf("backup with priority 20 must stay active: %#v", list.Items)
		}
	}

	// 另一个场地（网关 MAC 不同）想用同一个网段 → 必须拒绝
	other := registerM2BNode(t, app, admin.Token, "branch-office")
	addM2BMember(t, app, admin.Token, network.ID, other.NodeID)
	if err := app.db.Model(&models.Node{}).Where("id = ?", other.NodeID).
		Updates(map[string]any{"environment": "host", "gateway_on_physical": true, "gateway_mac": "aa:bb:cc:dd:ee:99"}).Error; err != nil {
		t.Fatalf("seed other gateway: %v", err)
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": other.NodeID, "cidr": "192.168.50.0/24", "enabled": true,
	})
	requireStatus(t, status, http.StatusConflict)
	if !strings.Contains(string(body), "route_conflict") {
		t.Fatalf("cross-site conflict expected, got %s", body)
	}

	// 人工纠错：同一条手动站点可以把「自动识别为不同站点」的节点并到一组
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": primary.NodeID, "cidr": "10.20.0.0/24", "site": "forced-site", "priority": 10, "enabled": true,
	})
	requireStatus(t, status, http.StatusCreated)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks/"+network.ID.String()+"/routes", admin.Token, map[string]any{
		"node_id": other.NodeID, "cidr": "10.20.0.0/24", "site": "forced-site", "priority": 20, "enabled": true,
	})
	requireStatus(t, status, http.StatusCreated)
	if !strings.Contains(string(body), "forced-site") {
		t.Fatalf("manual site should be persisted, got %s", body)
	}

	_ = json.Valid([]byte("{}"))
}
