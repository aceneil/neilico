package service

import (
	"sort"
	"strings"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
)

const (
	// DefaultRoutePriority 是未显式指定优先级时的取值。
	DefaultRoutePriority = 100
	MinRoutePriority     = 1
	MaxRoutePriority     = 999
)

// RouteConflictError 让「子网路由冲突」能以 409 带出可读原因
// （serviceError 对 ErrConflict 默认只回一句 resource conflict）。
type RouteConflictError struct {
	Reason string
}

func (e *RouteConflictError) Error() string { return e.Reason }
func (e *RouteConflictError) Unwrap() error { return ErrConflict }

// natGatewayMACs 是虚拟机 NAT 网关的固定 MAC：它们在每台 VM 上都一样，
// 不能作为「同一个实体局域网」的证据（否则同一台宿主上的两台 NAT 虚拟机被误合并）。
var natGatewayMACs = map[string]bool{
	"52:55:0a:00:02:02": true, // QEMU user-mode networking (slirp)
	"52:54:00:12:34:56": true, // libvirt default NAT bridge
}

// NodeSiteKey 自动识别节点所在站点：默认网关的 MAC 相同 = 同一台路由器 = 同一个二层域。
// 判据不成立时退回「该节点自成一个站点」——宁可不合并不误合并。
//
// 不成立的情形：Agent 在容器里（看到的是 docker bridge）、默认路由不在物理网卡上、
// 拿不到网关 MAC、或网关是虚拟机 NAT 网关。
func NodeSiteKey(node models.Node) string {
	// 判据是「默认路由走物理网卡」，而不是「不在容器里」：
	// docker --network host 的容器看到的正是宿主机网络（能看到物理网卡），
	// 而 bridge 容器的 veth 没有 /sys/class/net/<if>/device，天然被排除。
	if !node.GatewayOnPhysical {
		return "node:" + node.ID.String()
	}
	mac := strings.ToLower(strings.TrimSpace(node.GatewayMAC))
	if mac == "" || natGatewayMACs[mac] {
		return "node:" + node.ID.String()
	}
	return "gw:" + mac
}

// ResolveSite 返回路由最终归属的站点：**手动声明优先**（用于人工纠错/特殊拓扑），
// 没有手动声明时使用 NodeSiteKey 的自动识别结果。
func ResolveSite(explicit string, node models.Node) string {
	if site := strings.TrimSpace(explicit); site != "" {
		return "manual:" + site
	}
	return NodeSiteKey(node)
}

// RouteAvailability 表示某节点此刻能否承担它宣告的子网。
type RouteAvailability func(nodeID uuid.UUID) bool

// NodeAvailableForRouting 是「能不能当主路由」的判据：
//   - 必须 online（sweeper 会在心跳超时后置 offline）；
//   - Mesh 能力必须可用（tunnel/mesh/subnet_routes 三项全 ready）；
//   - 老 Agent 从未上报能力（mesh 为空）时按可用处理，避免升级后路由全灭。
func NodeAvailableForRouting(node models.Node) bool {
	if node.Status != NodeStatusOnline {
		return false
	}
	caps := node.Capabilities.Data()
	if caps.Unreported() {
		return true // 老 Agent / 刚注册：当作「还不知道」，不要因为缺能力上报把路由全熄了
	}
	return caps.MeshApplicable()
}

// SelectActiveRoutes 为每条 CIDR 选出当前应该生效的路由。
//
// 同一个 CIDR 允许挂在多台节点上（同一实体局域网的冗余）：优先在「可用」的候选里
// 按优先级数字最小者胜出（同优先级按 node_id 排序，结果稳定）。
// 没有任何可用候选时退回「全部候选里优先级最高的一条」作为最后已知归属——这样
// 单节点路由不会因为一次心跳抖动就从所有 peer 的 AllowedIPs 里消失（避免配置抖动，
// 实际效果与"不放路由"相同：包仍然进隧道后无对端可收）。
func SelectActiveRoutes(routes []models.SubnetRoute, available RouteAvailability) map[string]models.SubnetRoute {
	enabled := make([]models.SubnetRoute, 0, len(routes))
	for _, item := range routes {
		if item.Enabled {
			enabled = append(enabled, item)
		}
	}
	sort.SliceStable(enabled, func(i, j int) bool {
		if enabled[i].Priority != enabled[j].Priority {
			return enabled[i].Priority < enabled[j].Priority
		}
		return enabled[i].NodeID.String() < enabled[j].NodeID.String()
	})
	active := make(map[string]models.SubnetRoute, len(enabled))
	for _, item := range enabled {
		if _, exists := active[item.CIDR]; exists {
			continue // 已选过更优的
		}
		if available == nil || available(item.NodeID) {
			active[item.CIDR] = item
		}
	}
	// 第二轮：整组都不可用时，保留优先级最高的那条，避免配置反复抖动。
	for _, item := range enabled {
		if _, exists := active[item.CIDR]; exists {
			continue
		}
		active[item.CIDR] = item
	}
	return active
}

// ActiveRouteSignature 把「CIDR → 生效节点」压成字符串，用于判断归属是否变化
// （变化才需要 bump 配置版本，避免无意义地让所有 Agent 重新拉配置）。
func ActiveRouteSignature(active map[string]models.SubnetRoute) string {
	cidrs := make([]string, 0, len(active))
	for cidr := range active {
		cidrs = append(cidrs, cidr)
	}
	sort.Strings(cidrs)
	var builder strings.Builder
	for _, cidr := range cidrs {
		builder.WriteString(cidr)
		builder.WriteByte('=')
		builder.WriteString(active[cidr].NodeID.String())
		builder.WriteByte(';')
	}
	return builder.String()
}
