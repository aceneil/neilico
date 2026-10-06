package main

import (
	"testing"

	"neilico/control-plane/pkg/capabilities"
)

// 「能建接口就应用，失败则不应用」——门禁行为。
func TestShouldDryRunGate(t *testing.T) {
	tests := []struct {
		name      string
		requested bool
		reported  capabilities.Capabilities
		want      bool
	}{
		{
			name:      "all capabilities ready applies",
			requested: false,
			reported:  capabilities.Capabilities{Mesh: capabilities.MeshReady, SubnetRoutes: capabilities.Ready, Tunnel: capabilities.Ready},
			want:      false,
		},
		{
			name:      "tunnel unavailable blocks apply",
			requested: false,
			reported:  capabilities.Capabilities{Mesh: capabilities.MeshReady, SubnetRoutes: capabilities.Ready, Tunnel: capabilities.Unavailable, Reason: "无法创建 WireGuard 接口：operation not permitted"},
			want:      true,
		},
		{
			// 自相矛盾（mesh=ready 但 tunnel=unavailable）必须以更悲观者为准。
			name:      "contradictory capabilities block apply",
			requested: false,
			reported:  capabilities.Capabilities{Mesh: capabilities.MeshReady, SubnetRoutes: capabilities.Ready, Tunnel: capabilities.Unavailable, Reason: "未检测到可用的隧道/代理客户端"},
			want:      true,
		},
		{
			name:      "mesh degraded blocks apply",
			requested: false,
			reported:  capabilities.Capabilities{Mesh: capabilities.MeshDegraded, SubnetRoutes: capabilities.Ready, Tunnel: capabilities.Ready},
			want:      true,
		},
		{
			name:      "subnet routes unavailable blocks apply",
			requested: false,
			reported:  capabilities.Capabilities{Mesh: capabilities.MeshReady, SubnetRoutes: capabilities.Unavailable, Tunnel: capabilities.Ready},
			want:      true,
		},
		{
			name:      "explicit dry-run always wins",
			requested: true,
			reported:  capabilities.Capabilities{Mesh: capabilities.MeshReady, SubnetRoutes: capabilities.Ready, Tunnel: capabilities.Ready},
			want:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldDryRun(test.requested, test.reported); got != test.want {
				t.Fatalf("shouldDryRun(%v, %#v) = %v, want %v", test.requested, test.reported, got, test.want)
			}
		})
	}
}
