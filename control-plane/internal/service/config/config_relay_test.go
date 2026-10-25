package config

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	appconfig "neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service/cert"
)

type relayFixture struct {
	manager *Manager
	handle  *gorm.DB
	nodeA   models.Node
	nodeB   models.Node
}

// newRelayFixture 起一个只有两个节点的网络，返回配置管理器与两个节点。
func newRelayFixture(t *testing.T) relayFixture {
	t.Helper()
	handle, err := db.Open(appconfig.Database{
		Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared",
	}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := handle.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	crypto, err := cert.NewCryptoFromKey([]byte("relay-key"))
	if err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := crypto.Encrypt("NETWORK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	networkID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	nodeAID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	nodeBID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	joined := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tenant := models.Tenant{ID: tenantID, Name: "relay"}
	network := models.VirtualNetwork{ID: networkID, TenantID: tenantID, Name: "home", CIDR: "100.64.0.0/24", Secret: encryptedSecret}
	nodeA := models.Node{ID: nodeAID, TenantID: tenantID, Name: "node-a", PublicKey: "LOCAL_PUBLIC_KEY", OS: "linux", Arch: "amd64", Version: "test", Tags: []string{}, AgentTokenHash: "0000000000000000000000000000000000000000000000000000000000000001"}
	nodeB := models.Node{ID: nodeBID, TenantID: tenantID, Name: "node-b", PublicKey: "PEER_PUBLIC_KEY", PublicEndpoint: stringPointer("1.2.3.4:51820"), OS: "linux", Arch: "amd64", Version: "test", Tags: []string{}, AgentTokenHash: "0000000000000000000000000000000000000000000000000000000000000002"}
	memberA := models.NetworkMember{ID: uuid.MustParse("55555555-5555-5555-5555-555555555555"), NetworkID: networkID, NodeID: nodeAID, VirtualIP: "100.64.0.2", Role: "member", JoinedAt: joined}
	memberB := models.NetworkMember{ID: uuid.MustParse("66666666-6666-6666-6666-666666666666"), NetworkID: networkID, NodeID: nodeBID, VirtualIP: "100.64.0.3", Role: "member", JoinedAt: joined.Add(time.Second)}
	for _, record := range []any{&tenant, &network, &nodeA, &nodeB, &memberA, &memberB} {
		if err := handle.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	return relayFixture{manager: New(handle, crypto, nil), handle: handle, nodeA: nodeA, nodeB: nodeB}
}

// setDirectPath 直接写库改直连状态（绕开 GORM 对零值字段的默认值处理），
// 并把改完的节点重新读回来（配置生成用的是传入的节点结构体）。
func (f relayFixture) setDirectPath(t *testing.T, nodeID uuid.UUID, direct bool) models.Node {
	t.Helper()
	if err := f.handle.Model(&models.Node{}).Where("id = ?", nodeID).Update("direct_path", direct).Error; err != nil {
		t.Fatal(err)
	}
	var node models.Node
	if err := f.handle.Where("id = ?", nodeID).First(&node).Error; err != nil {
		t.Fatal(err)
	}
	return node
}

func (f relayFixture) peerOf(t *testing.T, node models.Node) Peer {
	t.Helper()
	config, err := f.manager.buildNodeConfig(context.Background(), f.handle, node)
	if err != nil {
		t.Fatalf("生成节点配置失败：%v", err)
	}
	if config.Network == nil {
		t.Fatal("网络配置为空")
	}
	for _, peer := range config.Network.Peers {
		if peer.PublicKey == "PEER_PUBLIC_KEY" {
			return peer
		}
	}
	t.Fatalf("没有找到对端 peer：%+v", config.Network.Peers)
	return Peer{}
}

// 直连可用时不下发中继字段（行为与中继上线前完全一致）。
func TestPeerConfigOmitsRelayWhenDirectPathWorks(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.manager.SetRelayEndpoint("relay.example.com:51821")
	peer := fixture.peerOf(t, fixture.nodeA)
	if peer.RelayEndpoint != "" || peer.RelayNodeID != "" {
		t.Fatalf("直连可用时不应下发中继信息：%+v", peer)
	}
	encoded, err := json.Marshal([]Peer{peer})
	if err != nil {
		t.Fatal(err)
	}
	if containsRelayField(string(encoded)) {
		t.Fatalf("直连可用时 JSON 里不应出现中继字段：%s", encoded)
	}
}

// 对端直连不通 → 下发中继地址与对端 node uuid。
func TestPeerConfigCarriesRelayWhenPeerDirectPathFails(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.manager.SetRelayEndpoint("relay.example.com:51821")
	fixture.setDirectPath(t, fixture.nodeB.ID, false)
	peer := fixture.peerOf(t, fixture.nodeA)
	if peer.RelayEndpoint != "relay.example.com:51821" {
		t.Fatalf("relay_endpoint = %q，期望 relay.example.com:51821", peer.RelayEndpoint)
	}
	if peer.RelayNodeID != fixture.nodeB.ID.String() {
		t.Fatalf("relay_node_id = %q，期望 %s", peer.RelayNodeID, fixture.nodeB.ID)
	}
	encoded, err := json.Marshal([]Peer{peer})
	if err != nil {
		t.Fatal(err)
	}
	if !containsRelayField(string(encoded)) {
		t.Fatalf("兜底时 JSON 里必须出现中继字段：%s", encoded)
	}
}

// 本机直连不通 → 同样给对端下发中继（至少一方不通就走中继）。
func TestPeerConfigCarriesRelayWhenLocalDirectPathFails(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.manager.SetRelayEndpoint("relay.example.com:51821")
	nodeA := fixture.setDirectPath(t, fixture.nodeA.ID, false)
	peer := fixture.peerOf(t, nodeA)
	if peer.RelayEndpoint != "relay.example.com:51821" {
		t.Fatalf("relay_endpoint = %q", peer.RelayEndpoint)
	}
	if peer.RelayNodeID != fixture.nodeB.ID.String() {
		t.Fatalf("relay_node_id = %q", peer.RelayNodeID)
	}
}

// 中继没启用（默认关闭）→ 即使直连不通也不下发中继字段。
func TestPeerConfigOmitsRelayWhenRelayDisabled(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.setDirectPath(t, fixture.nodeB.ID, false)
	peer := fixture.peerOf(t, fixture.nodeA)
	if peer.RelayEndpoint != "" || peer.RelayNodeID != "" {
		t.Fatalf("中继关闭时不应下发中继信息：%+v", peer)
	}
}

// 端到端：快照 → 投递，agent 拿到的就是带 relay_endpoint 的那份。
func TestDeliveryCarriesRelayEndpoint(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.manager.SetRelayEndpoint("relay.example.com:51821")
	fixture.setDirectPath(t, fixture.nodeB.ID, false)
	if _, err := fixture.manager.BumpForNode(context.Background(), fixture.nodeA.ID, "relay"); err != nil {
		t.Fatalf("生成配置版本失败：%v", err)
	}
	delivery, _, err := fixture.manager.Delivery(context.Background(), fixture.nodeA.ID, 0, false)
	if err != nil {
		t.Fatalf("生成投递失败：%v", err)
	}
	if delivery.Network == nil {
		t.Fatal("投递里没有网络配置")
	}
	encoded, err := json.Marshal(delivery.Network.Peers)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRelayField(string(encoded)) {
		t.Fatalf("投递里没有中继字段：%s", encoded)
	}
}

func containsRelayField(payload string) bool {
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return false
	}
	for _, item := range decoded {
		if _, ok := item["relay_endpoint"]; ok {
			return true
		}
		if _, ok := item["relay_node_id"]; ok {
			return true
		}
	}
	return false
}
