package capabilities

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	nodecapabilities "neilico/control-plane/pkg/capabilities"
)

type Probes struct {
	GOOS         string
	LookPath     func(string) (string, error)
	PathExists   func(string) bool
	HasAdmin     func() bool
	TunnelClient func() bool
}

func DefaultProbes() Probes {
	return Probes{
		GOOS: runtime.GOOS,
		LookPath: func(name string) (string, error) {
			return exec.LookPath(name)
		},
		PathExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		HasAdmin: hasAdminPrivileges,
		TunnelClient: func() bool {
			for _, name := range []string{"neilico-tunnel", "npc"} {
				if _, err := exec.LookPath(name); err == nil {
					return true
				}
			}
			return false
		},
	}
}

func Detect() nodecapabilities.Capabilities {
	return DetectWith(DefaultProbes())
}

// DetectWith probes the host without creating interfaces or changing routes.
// Tests inject these functions to cover hosts with and without WireGuard.
func DetectWith(probes Probes) nodecapabilities.Capabilities {
	probes = withDefaults(probes)
	result := nodecapabilities.Capabilities{
		Mesh:         nodecapabilities.MeshUnavailable,
		SubnetRoutes: nodecapabilities.Unavailable,
		Tunnel:       nodecapabilities.Unavailable,
	}
	var reasons []string
	admin := probes.HasAdmin()
	hasIP := commandAvailable(probes.LookPath, "ip")

	switch probes.GOOS {
	case "linux":
		hasWG := commandAvailable(probes.LookPath, "wg")
		hasTUN := probes.PathExists("/dev/net/tun")
		switch {
		case !hasWG && !hasTUN:
			result.Mesh = nodecapabilities.MeshUnavailable
			reasons = append(reasons, "缺 wg 工具", "无 TUN")
		case !hasWG:
			result.Mesh = nodecapabilities.MeshUnavailable
			reasons = append(reasons, "缺 wg 工具")
		case !hasTUN:
			result.Mesh = nodecapabilities.MeshUnavailable
			reasons = append(reasons, "无 TUN")
		case !admin:
			result.Mesh = nodecapabilities.MeshDegraded
			reasons = append(reasons, "需管理员权限")
		default:
			result.Mesh = nodecapabilities.MeshReady
		}
		if hasIP && admin {
			result.SubnetRoutes = nodecapabilities.Ready
		} else {
			if !hasIP {
				reasons = append(reasons, "缺 ip 路由工具")
			}
			if !admin {
				reasons = append(reasons, "改路由需 root/管理员权限")
			}
		}
	case "darwin":
		if wireGuardComponent(probes) {
			if !admin {
				result.Mesh = nodecapabilities.MeshDegraded
				reasons = append(reasons, "WireGuard 系统组件需管理员权限")
			} else {
				result.Mesh = nodecapabilities.MeshDegraded
				reasons = append(reasons, "检测到 WireGuard 系统组件，但当前 Agent 接口创建适配不支持 macOS")
			}
		} else {
			reasons = append(reasons, "缺少 macOS WireGuard 系统组件")
		}
		reasons = append(reasons, "平台不支持当前 Agent 的路由命令")
	case "windows":
		if wireGuardComponent(probes) {
			if !admin {
				result.Mesh = nodecapabilities.MeshDegraded
				reasons = append(reasons, "WireGuard 系统组件需管理员权限")
			} else {
				result.Mesh = nodecapabilities.MeshDegraded
				reasons = append(reasons, "检测到 WireGuard 系统组件，但当前 Agent 接口创建适配不支持 Windows")
			}
		} else {
			reasons = append(reasons, "缺少 Windows WireGuard 系统组件")
		}
		reasons = append(reasons, "平台不支持当前 Agent 的路由命令")
	default:
		reasons = append(reasons, "平台不支持")
	}

	if probes.TunnelClient() {
		result.Tunnel = nodecapabilities.Ready
	} else {
		reasons = append(reasons, "未检测到可用的隧道/代理客户端")
	}
	result.Reason = strings.Join(dedupe(reasons), " / ")
	return result
}

func withDefaults(probes Probes) Probes {
	if probes.GOOS == "" {
		probes.GOOS = runtime.GOOS
	}
	if probes.LookPath == nil {
		probes.LookPath = DefaultProbes().LookPath
	}
	if probes.PathExists == nil {
		probes.PathExists = DefaultProbes().PathExists
	}
	if probes.HasAdmin == nil {
		probes.HasAdmin = hasAdminPrivileges
	}
	if probes.TunnelClient == nil {
		probes.TunnelClient = func() bool { return false }
	}
	return probes
}

func commandAvailable(lookPath func(string) (string, error), name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func wireGuardComponent(probes Probes) bool {
	if commandAvailable(probes.LookPath, "wireguard-go") || commandAvailable(probes.LookPath, "wireguard.exe") || commandAvailable(probes.LookPath, "wg") {
		return true
	}
	switch probes.GOOS {
	case "darwin":
		return probes.PathExists("/Applications/WireGuard.app/Contents/MacOS/WireGuard") ||
			probes.PathExists("/Applications/WireGuard.app/Contents/MacOS/wireguard-go") ||
			probes.PathExists("/usr/local/bin/wireguard-go")
	case "windows":
		return probes.PathExists(`C:\Windows\System32\drivers\wireguard.sys`) ||
			probes.PathExists(`C:\Program Files\WireGuard\wireguard.exe`)
	default:
		return false
	}
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
