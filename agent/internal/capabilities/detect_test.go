package capabilities

import (
	"errors"
	"strings"
	"testing"

	nodecapabilities "neilico/control-plane/pkg/capabilities"
)

func TestDetectWithWireGuard(t *testing.T) {
	probes := Probes{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "ip" || name == "wg" {
				return "/usr/sbin/" + name, nil
			}
			return "", errors.New("not found")
		},
		PathExists: func(path string) bool { return path == "/dev/net/tun" },
		HasAdmin:   func() bool { return true },
	}
	got := DetectWith(probes)
	want := nodecapabilities.Capabilities{Mesh: "ready", SubnetRoutes: "ready", Tunnel: "ready"}
	if got != want {
		t.Fatalf("DetectWith() = %#v, want %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("valid capabilities rejected: %v", err)
	}
}

func TestDetectWithoutWireGuard(t *testing.T) {
	probes := Probes{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "ip" {
				return "/usr/sbin/ip", nil
			}
			return "", errors.New("not found")
		},
		PathExists:   func(string) bool { return false },
		HasAdmin:     func() bool { return true },
		TunnelClient: func() bool { return false },
	}
	got := DetectWith(probes)
	if got.Mesh != nodecapabilities.MeshUnavailable || got.SubnetRoutes != nodecapabilities.Ready || got.Tunnel != nodecapabilities.Unavailable {
		t.Fatalf("DetectWith() = %#v", got)
	}
	if !strings.Contains(got.Reason, "缺 wg 工具") || !strings.Contains(got.Reason, "无 TUN") {
		t.Fatalf("missing honest mesh reasons: %q", got.Reason)
	}
	// tunnel 的原因必须描述「为什么建不了接口」，而不是去怪没装客户端。
	if !strings.Contains(got.Reason, "无法创建 WireGuard 接口") ||
		!strings.Contains(got.Reason, "内核未提供 WireGuard 支持") {
		t.Fatalf("tunnel reason must reflect reality: %q", got.Reason)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("unavailable capabilities rejected: %v", err)
	}
}

func TestDetectDegradesWithoutAdmin(t *testing.T) {
	got := DetectWith(Probes{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "ip" || name == "wg" {
				return name, nil
			}
			return "", errors.New("not found")
		},
		PathExists: func(path string) bool { return path == "/dev/net/tun" },
		HasAdmin:   func() bool { return false },
	})
	if got.Mesh != nodecapabilities.MeshDegraded || got.SubnetRoutes != nodecapabilities.Unavailable {
		t.Fatalf("DetectWith() = %#v", got)
	}
	if !strings.Contains(got.Reason, "需管理员权限") {
		t.Fatalf("missing privilege reason: %q", got.Reason)
	}
	if got.Tunnel != nodecapabilities.Unavailable || !strings.Contains(got.Reason, "缺 CAP_NET_ADMIN") {
		t.Fatalf("tunnel without CAP_NET_ADMIN must be unavailable with a real reason: %#v %q", got, got.Reason)
	}
}

// 能真的建接口就必须判为可用——这正是旧实现漏掉的场景（内核 WG 可用、但没有外部
// 隧道客户端二进制），也是 wg0 永远建不起来的根因。
func TestDetectTunnelReadyWhenInterfaceCanBeCreated(t *testing.T) {
	got := DetectWith(Probes{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "ip" || name == "wg" {
				return name, nil
			}
			return "", errors.New("not found")
		},
		PathExists:         func(path string) bool { return path == "/dev/net/tun" },
		HasAdmin:           func() bool { return true },
		CanCreateInterface: func() error { return nil },
		TunnelClient:       func() bool { return false }, // 没有外部客户端，仍应可用
	})
	if got.Tunnel != nodecapabilities.Ready {
		t.Fatalf("tunnel = %q, want ready when the interface can be created (%#v)", got.Tunnel, got)
	}
	if got.Mesh != nodecapabilities.MeshReady {
		t.Fatalf("mesh = %q, want ready", got.Mesh)
	}
	if !got.MeshApplicable() {
		t.Fatalf("capabilities should be applicable: %#v", got)
	}
	if strings.Contains(got.Reason, "未检测到可用的隧道/代理客户端") {
		t.Fatalf("reason must not blame a missing client when the host can create interfaces: %q", got.Reason)
	}
	if got.Reason != "" {
		t.Fatalf("fully ready host should not carry a reason: %q", got.Reason)
	}
}

// 建不了接口时才判不可用，并且 reason 必须是真实的底层错误。
func TestDetectTunnelUnavailableWhenInterfaceCreationFails(t *testing.T) {
	probeErr := errors.New("ip link add dev neilico-p0001 type wireguard: Operation not permitted")
	got := DetectWith(Probes{
		GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "ip" || name == "wg" {
				return name, nil
			}
			return "", errors.New("not found")
		},
		PathExists:         func(path string) bool { return path == "/dev/net/tun" },
		HasAdmin:           func() bool { return true },
		CanCreateInterface: func() error { return probeErr },
	})
	if got.Tunnel != nodecapabilities.Unavailable {
		t.Fatalf("tunnel = %q, want unavailable", got.Tunnel)
	}
	if !strings.Contains(got.Reason, "Operation not permitted") {
		t.Fatalf("reason must carry the real probe error: %q", got.Reason)
	}
	if got.MeshApplicable() {
		t.Fatalf("mesh must not be applicable while the tunnel is unavailable: %#v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("unavailable capabilities rejected: %v", err)
	}
}

// 非 Linux 且没有真实探测时，才退回「外部客户端」兼容判断。
func TestDetectTunnelFallsBackToClientWithoutProbe(t *testing.T) {
	got := DetectWith(Probes{
		GOOS:         "linux",
		LookPath:     func(name string) (string, error) { return name, nil },
		PathExists:   func(string) bool { return true },
		HasAdmin:     func() bool { return true },
		TunnelClient: func() bool { return true },
	})
	if got.Tunnel != nodecapabilities.Ready {
		t.Fatalf("tunnel = %q, want ready via client fallback", got.Tunnel)
	}

	got = DetectWith(Probes{
		GOOS:         "darwin",
		LookPath:     func(name string) (string, error) { return name, nil },
		PathExists:   func(string) bool { return false },
		HasAdmin:     func() bool { return true },
		TunnelClient: func() bool { return false },
	})
	if got.Tunnel != nodecapabilities.Unavailable {
		t.Fatalf("tunnel = %q, want unavailable", got.Tunnel)
	}
	if !strings.Contains(got.Reason, "未检测到可用的隧道/代理客户端") {
		t.Fatalf("fallback reason = %q", got.Reason)
	}
}
