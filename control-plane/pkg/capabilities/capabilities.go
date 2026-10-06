package capabilities

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MeshReady       = "ready"
	MeshDegraded    = "degraded"
	MeshUnavailable = "unavailable"

	Ready       = "ready"
	Unavailable = "unavailable"
)

// Capabilities is the capability snapshot reported by an agent during
// enrollment and every heartbeat. Reason is required whenever a capability is
// not ready so the control plane never presents a partial agent as fully wired.
type Capabilities struct {
	Mesh         string `json:"mesh"`
	SubnetRoutes string `json:"subnet_routes"`
	Tunnel       string `json:"tunnel"`
	Reason       string `json:"reason"`
}

func Unknown(reason string) Capabilities {
	return Capabilities{
		Mesh:         MeshUnavailable,
		SubnetRoutes: Unavailable,
		Tunnel:       Unavailable,
		Reason:       reason,
	}
}

// Normalize 把「尚未上报」的空值补成明确状态。
// 用途：AutoMigrate 给已有部署新增 capabilities 列时只能用 default '{}'
// （GORM tag 里塞不进带引号的 JSON），老行的该列因此是空对象；
// 读取边界调用本方法后，界面/接口不会显示成空白。
func (c Capabilities) Normalize() Capabilities {
	if c.Mesh == "" {
		c.Mesh = MeshUnavailable
	}
	if c.SubnetRoutes == "" {
		c.SubnetRoutes = Unavailable
	}
	if c.Tunnel == "" {
		c.Tunnel = Unavailable
	}
	if strings.TrimSpace(c.Reason) == "" {
		c.Reason = "尚未上报"
	}
	return c
}

func (c Capabilities) Validate() error {
	reason := strings.TrimSpace(c.Reason)
	switch c.Mesh {
	case MeshReady, MeshDegraded, MeshUnavailable:
	default:
		return fmt.Errorf("capabilities.mesh must be ready, degraded, or unavailable")
	}
	switch c.SubnetRoutes {
	case Ready, Unavailable:
	default:
		return fmt.Errorf("capabilities.subnet_routes must be ready or unavailable")
	}
	switch c.Tunnel {
	case Ready, Unavailable:
	default:
		return fmt.Errorf("capabilities.tunnel must be ready or unavailable")
	}
	notReady := c.Mesh != MeshReady || c.SubnetRoutes != Ready || c.Tunnel != Ready
	if notReady && reason == "" {
		return errors.New("capabilities.reason is required when a capability is not ready")
	}
	return nil
}

func (c Capabilities) MeshReady() bool {
	return c.Mesh == MeshReady
}

func (c Capabilities) HasUnavailable() bool {
	return c.Mesh != MeshReady || c.SubnetRoutes != Ready || c.Tunnel != Ready
}

// MeshApplicable 报告该节点是否真的可以应用 mesh 配置：三项承载能力都必须 ready。
// tunnel 由 agent 的真实建接口探测得出，是权威信号——tunnel 不可用时即便 mesh 自报
// ready 也不得应用（那正是「宿主上永远没有 wg0」的根因）。
func (c Capabilities) MeshApplicable() bool {
	return c.Mesh == MeshReady && c.SubnetRoutes == Ready && c.Tunnel == Ready
}

// severity 把能力状态映射为「悲观等级」：数值越大越差。
// MeshReady 与 Ready 同为 "ready"，故只列一次。
func severity(status string) int {
	switch status {
	case MeshReady:
		return 0
	case MeshDegraded:
		return 1
	default:
		return 2
	}
}

func normalizeMeshStatus(status string) string {
	switch status {
	case MeshReady, MeshDegraded, MeshUnavailable:
		return status
	default:
		return MeshUnavailable
	}
}

// EffectiveMesh 返回节点视图应采用的「有效 Mesh 状态」。
//
// 规则：以 tunnel 为权威信号——tunnel 不可用 → Mesh 不可用；并以更悲观者为准，
// 从而避免同一对象里 mesh=ready 与 tunnel=unavailable 自相矛盾时，
// 前端只展示 mesh 而把隧道故障掩盖成绿色就绪。
func (c Capabilities) EffectiveMesh() string {
	effective := normalizeMeshStatus(c.Mesh)
	if severity(c.Tunnel) > severity(effective) {
		effective = normalizeMeshStatus(c.Tunnel)
	}
	return effective
}

// Contradictory 报告 capabilities 自相矛盾：mesh 声称的状态优于 tunnel
// （典型：mesh=ready 但 tunnel=unavailable）。出现时应按更悲观者展示并注明。
func (c Capabilities) Contradictory() bool {
	return severity(c.Mesh) < severity(c.Tunnel)
}

// Note 给出需要向运维显式说明的能力异常（无异常时返回空串）。
func (c Capabilities) Note() string {
	if c.Contradictory() {
		return fmt.Sprintf("capabilities 自相矛盾：mesh=%s 但 tunnel=%s；已按更悲观的有效状态 %s 展示", c.Mesh, c.Tunnel, c.EffectiveMesh())
	}
	if c.Tunnel != Ready {
		return "隧道不可用：Mesh 不可用"
	}
	return ""
}
