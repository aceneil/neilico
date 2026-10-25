package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
)

// fakeBumper 记录配置版本刷新调用。
type fakeBumper struct {
	networks []uuid.UUID
}

func (f *fakeBumper) BumpForNetwork(ctx context.Context, networkID uuid.UUID, reason string) (models.ConfigVersion, error) {
	f.networks = append(f.networks, networkID)
	return models.ConfigVersion{}, nil
}

func newCandidateTestService(t *testing.T) (*NodeService, models.Node, string) {
	t.Helper()
	handle, err := db.Open(config.Database{
		Driver: "sqlite",
		DSN:    "file:" + uuid.NewString() + "?mode=memory&cache=shared",
	}, "error")
	if err != nil {
		t.Fatalf("打开测试库：%v", err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatalf("迁移测试库：%v", err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := handle.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	tenant := models.Tenant{ID: uuid.New(), Name: "cand-" + uuid.NewString()[:8], CreatedAt: time.Now().UTC()}
	if err := handle.Create(&tenant).Error; err != nil {
		t.Fatalf("建租户：%v", err)
	}
	token, tokenHash, err := auth.GenerateAgentToken()
	if err != nil {
		t.Fatalf("生成 agent token：%v", err)
	}
	node := models.Node{
		ID: uuid.New(), TenantID: tenant.ID, Name: "node-a", PublicKey: "pubkey",
		OS: "linux", Arch: "amd64", Version: "0.1.0", Status: "offline",
		Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: tokenHash,
		CreatedAt: time.Now().UTC(),
	}
	if err := handle.Create(&node).Error; err != nil {
		t.Fatalf("建节点：%v", err)
	}
	return NewNodeService(handle, time.Minute), node, token
}

func TestHeartbeatStoresCandidates(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	input := HeartbeatInput{
		Version: "0.2.0",
		Candidates: []models.Candidate{
			{Kind: "lan", Addr: "192.168.1.5:51820"},
			{Kind: "stun", Addr: "203.0.113.9:51820"},
			{Kind: "public", Addr: "198.51.100.9:51820"},
		},
	}
	updated, err := svc.Heartbeat(context.Background(), node.ID, token, input)
	if err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if len(updated.Candidates) != 3 {
		t.Fatalf("落库的候选数量 = %d，期望 3（%+v）", len(updated.Candidates), updated.Candidates)
	}
	var stored models.Node
	if err := svc.db.First(&stored, "id = ?", node.ID).Error; err != nil {
		t.Fatalf("读取节点失败：%v", err)
	}
	if len(stored.Candidates) != 3 {
		t.Fatalf("数据库里的候选 = %+v，期望 3 条", stored.Candidates)
	}
	found := false
	for _, candidate := range stored.Candidates {
		if candidate.Kind == "stun" && candidate.Addr == "203.0.113.9:51820" {
			found = true
		}
	}
	if !found {
		t.Fatalf("数据库里没有 stun 候选：%+v", stored.Candidates)
	}
	// 心跳不得影响既有的 endpoint 逻辑。
	if stored.PublicEndpoint != nil {
		t.Fatalf("心跳不应改动 public endpoint：%v", *stored.PublicEndpoint)
	}
}

func TestHeartbeatWithoutCandidatesKeepsBehavior(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	// 老版本 agent：心跳里没有 candidates 字段。
	if _, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{Version: "0.1.0"}); err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	var stored models.Node
	if err := svc.db.First(&stored, "id = ?", node.ID).Error; err != nil {
		t.Fatalf("读取节点失败：%v", err)
	}
	if len(stored.Candidates) != 0 {
		t.Fatalf("老 agent 不带候选时不应写入候选：%+v", stored.Candidates)
	}
}

func TestHeartbeatRejectsInvalidCandidates(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	input := HeartbeatInput{
		Candidates: []models.Candidate{
			{Kind: "bogus", Addr: "203.0.113.9:51820"},
			{Kind: "stun", Addr: "not-an-addr"},
			{Kind: "stun", Addr: "203.0.113.9:51820"},
			{Kind: "stun", Addr: "203.0.113.9:51820"},
		},
	}
	updated, err := svc.Heartbeat(context.Background(), node.ID, token, input)
	if err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if len(updated.Candidates) != 1 {
		t.Fatalf("清洗后的候选 = %+v，期望只保留 1 条合法候选", updated.Candidates)
	}
	if updated.Candidates[0].Kind != "stun" || updated.Candidates[0].Addr != "203.0.113.9:51820" {
		t.Fatalf("清洗后的候选不正确：%+v", updated.Candidates[0])
	}
}

func TestHeartbeatCandidateChangeBumpsNetworkConfig(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	bumper := &fakeBumper{}
	svc.SetConfigBumper(bumper)

	network := models.VirtualNetwork{ID: uuid.New(), TenantID: node.TenantID, Name: "net", CIDR: "100.64.9.0/24", CreatedAt: time.Now().UTC()}
	if err := svc.db.Create(&network).Error; err != nil {
		t.Fatalf("建网络：%v", err)
	}
	member := models.NetworkMember{ID: uuid.New(), NetworkID: network.ID, NodeID: node.ID, VirtualIP: "100.64.9.2", Role: "member", JoinedAt: time.Now().UTC()}
	if err := svc.db.Create(&member).Error; err != nil {
		t.Fatalf("建网络成员：%v", err)
	}

	// 第一次心跳带候选 → 候选从无到有 → 需要刷新配置。
	if _, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{
		Candidates: []models.Candidate{{Kind: "stun", Addr: "203.0.113.9:51820"}},
	}); err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if len(bumper.networks) != 1 || bumper.networks[0] != network.ID {
		t.Fatalf("候选变化后应当刷新所在网络的配置，实际 %v", bumper.networks)
	}

	// 第二次心跳候选没变 → 不再刷新（避免每次心跳都把配置版本顶上去）。
	if _, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{
		Candidates: []models.Candidate{{Kind: "stun", Addr: "203.0.113.9:51820"}},
	}); err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if len(bumper.networks) != 1 {
		t.Fatalf("候选没变时不应刷新配置，实际刷新了 %d 次", len(bumper.networks))
	}

	// 候选变了 → 再次刷新。
	if _, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{
		Candidates: []models.Candidate{{Kind: "stun", Addr: "203.0.113.10:51820"}},
	}); err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if len(bumper.networks) != 2 {
		t.Fatalf("候选变化后应当再次刷新配置，实际刷新了 %d 次", len(bumper.networks))
	}
}
