package config

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	appconfig "neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service/cert"
)

// 候选地址必须随配置下发给对端：agent 靠它做 UDP 打洞选路
// （stun → public → lan，同网段的 lan 优先）。
func TestPeerConfigCarriesCandidates(t *testing.T) {
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
	crypto, err := cert.NewCryptoFromKey([]byte("candidates-key"))
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
	tenant := models.Tenant{ID: tenantID, Name: "cand"}
	network := models.VirtualNetwork{ID: networkID, TenantID: tenantID, Name: "home", CIDR: "100.64.0.0/24", Secret: encryptedSecret}
	nodeA := models.Node{ID: nodeAID, TenantID: tenantID, Name: "node-a", PublicKey: "LOCAL_PUBLIC_KEY", OS: "linux", Arch: "amd64", Version: "test", Tags: []string{}, AgentTokenHash: "0000000000000000000000000000000000000000000000000000000000000001"}
	nodeB := models.Node{
		ID: nodeBID, TenantID: tenantID, Name: "node-b", PublicKey: "PEER_PUBLIC_KEY",
		PublicEndpoint: stringPointer("1.2.3.4:51820"), OS: "linux", Arch: "amd64", Version: "test",
		Tags: []string{}, AgentTokenHash: "0000000000000000000000000000000000000000000000000000000000000002",
		Candidates: datatypes.JSONSlice[models.Candidate]{
			{Kind: "stun", Addr: "1.2.3.4:51820"},
			{Kind: "public", Addr: "5.6.7.8:51820"},
			{Kind: "lan", Addr: "192.168.1.5:51820"},
		},
	}
	memberA := models.NetworkMember{ID: uuid.MustParse("55555555-5555-5555-5555-555555555555"), NetworkID: networkID, NodeID: nodeAID, VirtualIP: "100.64.0.2", Role: "member", JoinedAt: joined}
	memberB := models.NetworkMember{ID: uuid.MustParse("66666666-6666-6666-6666-666666666666"), NetworkID: networkID, NodeID: nodeBID, VirtualIP: "100.64.0.3", Role: "member", JoinedAt: joined.Add(time.Second)}
	for _, record := range []any{&tenant, &network, &nodeA, &nodeB, &memberA, &memberB} {
		if err := handle.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	manager := New(handle, crypto, nil)
	config, err := manager.buildNodeConfig(context.Background(), handle, nodeA)
	if err != nil {
		t.Fatal(err)
	}
	if config.Network == nil {
		t.Fatal("网络配置为空")
	}
	var peer *Peer
	for index := range config.Network.Peers {
		if config.Network.Peers[index].PublicKey == "PEER_PUBLIC_KEY" {
			peer = &config.Network.Peers[index]
		}
	}
	if peer == nil {
		t.Fatalf("没有找到对端 peer：%+v", config.Network.Peers)
	}
	if len(peer.Candidates) != 3 {
		t.Fatalf("对端候选 = %+v，期望 3 条", peer.Candidates)
	}
	if peer.Candidates[0].Kind != "stun" || peer.Candidates[0].Addr != "1.2.3.4:51820" {
		t.Fatalf("对端候选内容不对：%+v", peer.Candidates)
	}
	// 既有字段不能受影响。
	if peer.Endpoint != "1.2.3.4:51820" {
		t.Fatalf("对端 endpoint = %q，期望保持原样", peer.Endpoint)
	}

	// 下发的 JSON 里必须能看到 candidates（agent 靠它打洞）。
	encoded, err := json.Marshal(config.Network.Peers)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range decoded {
		if item["public_key"] != "PEER_PUBLIC_KEY" {
			continue
		}
		candidates, ok := item["candidates"].([]any)
		if !ok || len(candidates) != 3 {
			t.Fatalf("下发的 JSON 里没有对端 candidates：%s", encoded)
		}
		found = true
	}
	if !found {
		t.Fatalf("下发的 JSON 里没有找到对端：%s", encoded)
	}

	// 端到端：配置快照 → 投递（agent 拿到的就是它）。
	if _, err := manager.BumpForNode(context.Background(), nodeAID, "candidates"); err != nil {
		t.Fatalf("生成配置版本失败：%v", err)
	}
	delivery, _, err := manager.Delivery(context.Background(), nodeAID, 0, false)
	if err != nil {
		t.Fatalf("生成投递失败：%v", err)
	}
	if delivery.Network == nil {
		t.Fatal("投递里没有网络配置")
	}
	delivered, err := json.Marshal(delivery.Network.Peers)
	if err != nil {
		t.Fatal(err)
	}
	if !containsCandidateField(string(delivered)) {
		t.Fatalf("投递里没有对端 candidates：%s", delivered)
	}
}

// 老版本 agent 不上报候选 → 下发的配置里不能凭空出现 candidates 字段。
func TestPeerConfigOmitsCandidatesWhenAbsent(t *testing.T) {
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
	crypto, err := cert.NewCryptoFromKey([]byte("candidates-key-2"))
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
	tenant := models.Tenant{ID: tenantID, Name: "cand"}
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
	manager := New(handle, crypto, nil)
	config, err := manager.buildNodeConfig(context.Background(), handle, nodeA)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config.Network.Peers)
	if err != nil {
		t.Fatal(err)
	}
	if containsCandidateField(string(encoded)) {
		t.Fatalf("没有候选时不应下发 candidates 字段：%s", encoded)
	}
}

func containsCandidateField(payload string) bool {
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return false
	}
	for _, item := range decoded {
		if _, ok := item["candidates"]; ok {
			return true
		}
	}
	return false
}
