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
		PathExists:   func(path string) bool { return path == "/dev/net/tun" },
		HasAdmin:     func() bool { return true },
		TunnelClient: func() bool { return true },
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
	if !strings.Contains(got.Reason, "缺 wg 工具") || !strings.Contains(got.Reason, "无 TUN") ||
		!strings.Contains(got.Reason, "未检测到可用的隧道/代理客户端") {
		t.Fatalf("missing honest reasons: %q", got.Reason)
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
		PathExists:   func(path string) bool { return path == "/dev/net/tun" },
		HasAdmin:     func() bool { return false },
		TunnelClient: func() bool { return true },
	})
	if got.Mesh != nodecapabilities.MeshDegraded || got.SubnetRoutes != nodecapabilities.Unavailable {
		t.Fatalf("DetectWith() = %#v", got)
	}
	if !strings.Contains(got.Reason, "需管理员权限") {
		t.Fatalf("missing privilege reason: %q", got.Reason)
	}
}
