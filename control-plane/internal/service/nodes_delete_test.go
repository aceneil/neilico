package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
)

// 真实事故：删除一个已加入虚拟网络的节点会返回 HTTP 500，原因是
// network_members.node_id 对 nodes 是 ON DELETE RESTRICT：
//
//	update or delete on table "nodes" violates foreign key constraint "fk_network_members_node"
//
// 同一问题也适用于 subnet_routes 与 traffic_logs（同为 RESTRICT）。
// 单测默认的 sqlite 不启用外键约束，所以这条路径必须显式开 _pragma=foreign_keys(1) 才能复现，
// 否则测试对这类缺陷毫无判别力。
func TestDeleteNodeDetachesRestrictedReferences(t *testing.T) {
	handle, err := db.Open(config.Database{
		Driver: "sqlite",
		DSN:    "file:" + uuid.NewString() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)",
	}, "error")
	if err != nil {
		t.Fatalf("打开测试库：%v", err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatalf("迁移测试库：%v", err)
	}

	tenant := models.Tenant{ID: uuid.New(), Name: "del-" + uuid.NewString()[:8], CreatedAt: time.Now().UTC()}
	if err := handle.Create(&tenant).Error; err != nil {
		t.Fatalf("建租户：%v", err)
	}
	network := models.VirtualNetwork{ID: uuid.New(), TenantID: tenant.ID, Name: "net-" + uuid.NewString()[:8], CIDR: "100.64.31.0/24", CreatedAt: time.Now().UTC()}
	if err := handle.Create(&network).Error; err != nil {
		t.Fatalf("建网络：%v", err)
	}
	node := models.Node{
		ID: uuid.New(), TenantID: tenant.ID, Name: "victim", PublicKey: "pubkey",
		OS: "linux", Arch: "amd64", Version: "0.1.0", Status: "offline",
		Tags: datatypes.JSONSlice[string]{}, AgentTokenHash: uuid.NewString()[:32],
		CreatedAt: time.Now().UTC(),
	}
	if err := handle.Create(&node).Error; err != nil {
		t.Fatalf("建节点：%v", err)
	}
	member := models.NetworkMember{ID: uuid.New(), NetworkID: network.ID, NodeID: node.ID, VirtualIP: "100.64.31.2", Role: "member", JoinedAt: time.Now().UTC()}
	if err := handle.Create(&member).Error; err != nil {
		t.Fatalf("建网络成员：%v", err)
	}
	subnet := models.SubnetRoute{ID: uuid.New(), NetworkID: network.ID, NodeID: node.ID, CIDR: "192.168.44.0/24", Enabled: true}
	if err := handle.Create(&subnet).Error; err != nil {
		t.Fatalf("建子网路由：%v", err)
	}
	traffic := models.TrafficLog{ID: uuid.New(), TenantID: tenant.ID, NodeID: node.ID, Direction: "in", Bytes: 1024, Protocol: "tcp", Peer: "10.0.0.1", CreatedAt: time.Now().UTC()}
	if err := handle.Create(&traffic).Error; err != nil {
		t.Fatalf("建流量日志：%v", err)
	}

	svc := NewNodeService(handle, time.Minute)
	if err := svc.Delete(context.Background(), node.ID, nil); err != nil {
		t.Fatalf("删除节点失败（RESTRICT 外键没清理就会走到这里，真机表现为 HTTP 500）：%v", err)
	}

	assertCount(t, handle, &models.Node{}, "id = ?", node.ID, 0, "节点")
	assertCount(t, handle, &models.NetworkMember{}, "node_id = ?", node.ID, 0, "网络成员")
	assertCount(t, handle, &models.SubnetRoute{}, "node_id = ?", node.ID, 0, "子网路由")
	assertCount(t, handle, &models.TrafficLog{}, "node_id = ?", node.ID, 0, "流量日志")

	// 租户与网络本身不应被牵连删除（RESTRICT 是「不许删被引用者」，不是级联）
	assertCount(t, handle, &models.Tenant{}, "id = ?", tenant.ID, 1, "租户")
	assertCount(t, handle, &models.VirtualNetwork{}, "id = ?", network.ID, 1, "网络")
}

func assertCount(t *testing.T, handle *gorm.DB, model any, where string, arg any, want int64, label string) {
	t.Helper()
	var got int64
	if err := handle.Model(model).Where(where, arg).Count(&got).Error; err != nil {
		t.Fatalf("统计%s失败：%v", label, err)
	}
	if got != want {
		t.Fatalf("%s 数量 = %d，期望 %d", label, got, want)
	}
}
