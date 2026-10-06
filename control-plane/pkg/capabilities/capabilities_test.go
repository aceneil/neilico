package capabilities

import (
	"strings"
	"testing"
)

func TestEffectiveMeshTunnelWins(t *testing.T) {
	// 自相矛盾：mesh=ready 但 tunnel=unavailable → 有效状态必须是不堪的 unavailable，
	// 绝不能让 mesh 把它掩盖成 ready。
	contradictory := Capabilities{Mesh: MeshReady, SubnetRoutes: Ready, Tunnel: Unavailable, Reason: "未检测到可用的隧道/代理客户端"}
	if got := contradictory.EffectiveMesh(); got != MeshUnavailable {
		t.Fatalf("EffectiveMesh() = %q, want %q", got, MeshUnavailable)
	}
	if !contradictory.Contradictory() {
		t.Fatalf("contradictory capabilities not detected: %#v", contradictory)
	}
	if !contradictory.HasUnavailable() {
		t.Fatalf("HasUnavailable() = false, want true")
	}
	if contradictory.MeshApplicable() {
		t.Fatalf("MeshApplicable() = true with tunnel unavailable")
	}
	if note := contradictory.Note(); !strings.Contains(note, "自相矛盾") {
		t.Fatalf("Note() = %q, want contradiction note", note)
	}

	// 一致就绪 → ready，且不应有任何告警说明。
	healthy := Capabilities{Mesh: MeshReady, SubnetRoutes: Ready, Tunnel: Ready}
	if got := healthy.EffectiveMesh(); got != MeshReady {
		t.Fatalf("EffectiveMesh() = %q, want ready", got)
	}
	if healthy.Contradictory() || healthy.Note() != "" || !healthy.MeshApplicable() {
		t.Fatalf("healthy capabilities flagged: contradictory=%v note=%q applicable=%v", healthy.Contradictory(), healthy.Note(), healthy.MeshApplicable())
	}
}

func TestEffectiveMeshPessimistic(t *testing.T) {
	tests := []struct {
		name string
		caps Capabilities
		want string
	}{
		{name: "mesh degraded tunnel ready stays degraded", caps: Capabilities{Mesh: MeshDegraded, Tunnel: Ready}, want: MeshDegraded},
		{name: "mesh degraded tunnel unavailable becomes unavailable", caps: Capabilities{Mesh: MeshDegraded, Tunnel: Unavailable}, want: MeshUnavailable},
		{name: "empty values normalize to unavailable", caps: Capabilities{}, want: MeshUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.caps.EffectiveMesh(); got != test.want {
				t.Fatalf("EffectiveMesh() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMeshApplicableRequiresAllCapabilities(t *testing.T) {
	if (Capabilities{Mesh: MeshReady, SubnetRoutes: Ready, Tunnel: Ready}).MeshApplicable() != true {
		t.Fatalf("fully ready capabilities should be applicable")
	}
	if (Capabilities{Mesh: MeshReady, SubnetRoutes: Unavailable, Tunnel: Ready}).MeshApplicable() {
		t.Fatalf("subnet_routes unavailable must block applicability")
	}
}
