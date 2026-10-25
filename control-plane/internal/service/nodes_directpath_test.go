package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
)

// 心跳上报 direct_path=false 必须落库（这是中继兜底唯一的触发信号）。
func TestHeartbeatStoresDirectPath(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	direct := false
	updated, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{Version: "0.2.0", DirectPath: &direct})
	if err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if updated.DirectPath {
		t.Fatal("direct_path=false 应该被记录下来")
	}
	// 再心跳一次 true → 应该改回 true。
	direct = true
	updated, err = svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{Version: "0.2.0", DirectPath: &direct})
	if err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if !updated.DirectPath {
		t.Fatal("direct_path=true 应该被记录下来")
	}
}

// 老版本 agent 不上报 direct_path → 保持库里的原值不变（向后兼容）。
func TestHeartbeatKeepsDirectPathWhenAbsent(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	direct := false
	if _, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{Version: "0.2.0", DirectPath: &direct}); err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	updated, err := svc.Heartbeat(context.Background(), node.ID, token, HeartbeatInput{Version: "0.2.0"})
	if err != nil {
		t.Fatalf("心跳失败：%v", err)
	}
	if updated.DirectPath {
		t.Fatal("没上报 direct_path 时不应该覆盖原来的值")
	}
}

// 中继会话的鉴权走的是和心跳一样的路径：node id + agent token。
func TestAuthenticateRelay(t *testing.T) {
	svc, node, token := newCandidateTestService(t)
	if err := svc.AuthenticateRelay(context.Background(), node.ID, token); err != nil {
		t.Fatalf("正确的 token 应该通过鉴权：%v", err)
	}
	if err := svc.AuthenticateRelay(context.Background(), node.ID, "wrong-token"); err == nil {
		t.Fatal("错误的 token 必须被拒绝")
	}
	if err := svc.AuthenticateRelay(context.Background(), node.ID, ""); err == nil {
		t.Fatal("空 token 必须被拒绝")
	}
	if err := svc.AuthenticateRelay(context.Background(), uuid.New(), token); err == nil {
		t.Fatal("不存在的节点必须被拒绝")
	}
	var _ models.Node = node
}
