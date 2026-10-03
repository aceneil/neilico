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
