package service

import (
	"testing"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
)

func routeRow(nodeID uuid.UUID, cidr string, priority int, enabled bool) models.SubnetRoute {
	return models.SubnetRoute{ID: uuid.New(), NodeID: nodeID, CIDR: cidr, Priority: priority, Enabled: enabled}
}

// 同一 CIDR 的多条宣告：只有优先级最高且可用的那条生效。
func TestSelectActiveRoutesPrefersLowestPriority(t *testing.T) {
	primary := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	backup := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	routes := []models.SubnetRoute{
		routeRow(backup, "192.168.1.0/24", 20, true),
		routeRow(primary, "192.168.1.0/24", 10, true),
	}
	active := SelectActiveRoutes(routes, func(uuid.UUID) bool { return true })
	if got := active["192.168.1.0/24"].NodeID; got != primary {
		t.Fatalf("active owner = %s, want primary %s", got, primary)
	}
}

// 主节点不可用时，备用接管。
func TestSelectActiveRoutesFailsOverToBackup(t *testing.T) {
	primary := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	backup := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	routes := []models.SubnetRoute{
		routeRow(primary, "192.168.1.0/24", 10, true),
		routeRow(backup, "192.168.1.0/24", 20, true),
	}
	active := SelectActiveRoutes(routes, func(nodeID uuid.UUID) bool { return nodeID == backup })
	if got := active["192.168.1.0/24"].NodeID; got != backup {
		t.Fatalf("active owner = %s, want backup %s", got, backup)
	}
}

// 全部不可用：退回优先级最高的那条作为最后已知归属（避免配置抖动），
// 而不是让 CIDR 从所有 peer 的 AllowedIPs 里消失。
func TestSelectActiveRoutesFallsBackWhenAllDown(t *testing.T) {
	primary := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	backup := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	routes := []models.SubnetRoute{
		routeRow(backup, "192.168.1.0/24", 20, true),
		routeRow(primary, "192.168.1.0/24", 10, true),
	}
	active := SelectActiveRoutes(routes, func(uuid.UUID) bool { return false })
	if got := active["192.168.1.0/24"].NodeID; got != primary {
		t.Fatalf("fallback owner = %s, want primary %s", got, primary)
	}
}

// 停用的宣告不参与竞争；同优先级按 node_id 稳定排序。
func TestSelectActiveRoutesIgnoresDisabledAndBreaksTies(t *testing.T) {
	first := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	second := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	routes := []models.SubnetRoute{
		routeRow(first, "192.168.9.0/24", 100, true),
		routeRow(second, "192.168.9.0/24", 100, true),
		routeRow(second, "192.168.7.0/24", 5, false),
	}
	active := SelectActiveRoutes(routes, func(uuid.UUID) bool { return true })
	if got := active["192.168.9.0/24"].NodeID; got != first {
		t.Fatalf("tie-break owner = %s, want %s", got, first)
	}
	if _, exists := active["192.168.7.0/24"]; exists {
		t.Fatal("disabled route must not become active")
	}
}

// 签名只随归属变化：同一归属的重复计算必须稳定（用于避免无意义 bump）。
func TestActiveRouteSignatureIsStable(t *testing.T) {
	node := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	a := map[string]models.SubnetRoute{"192.168.1.0/24": routeRow(node, "192.168.1.0/24", 10, true)}
	b := map[string]models.SubnetRoute{"192.168.1.0/24": routeRow(node, "192.168.1.0/24", 10, true)}
	if ActiveRouteSignature(a) != ActiveRouteSignature(b) {
		t.Fatal("signature must be deterministic")
	}
	if ActiveRouteSignature(a) == "" {
		t.Fatal("signature must include the cidr -> owner mapping")
	}
}

// 能力不健康（tunnel 不可用）的节点不能当主路由；老 Agent 未上报能力时按可用处理。
func TestNodeAvailableForRouting(t *testing.T) {
	online := models.Node{Status: NodeStatusOnline}
	if !NodeAvailableForRouting(online) {
		t.Fatal("online node without capability report should stay eligible (backward compatible)")
	}
	offline := models.Node{Status: NodeStatusOffline}
	if NodeAvailableForRouting(offline) {
		t.Fatal("offline node must never be eligible")
	}
}
