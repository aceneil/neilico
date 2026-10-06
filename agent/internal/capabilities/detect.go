package capabilities

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	nodecapabilities "neilico/control-plane/pkg/capabilities"
)

type Probes struct {
	GOOS       string
	LookPath   func(string) (string, error)
	PathExists func(string) bool
	HasAdmin   func() bool
	// CanCreateInterface 做一次真实、可回滚的探测：在本机创建（随后删除）一个
	// 一次性的 WireGuard 接口。它是 tunnel 能力的**权威信号**——外部「隧道/代理
	// 客户端」二进制（neilico-tunnel/npc）是否存在，根本不能证明本机能否承载
	// mesh 流量。nil 表示当前平台没有可用的真实探测（退化为静态判断）。
	CanCreateInterface func() error
	// TunnelClient 是兼容信号：仍有部署通过外部用户态客户端提供连通性。
	// 仅在没有真实探测可用时才参考它，绝不再把它当作 tunnel 是否可用的唯一依据。
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
		HasAdmin:           hasAdminPrivileges,
		CanCreateInterface: defaultInterfaceProbe,
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

// DetectWith probes the host. On Linux the tunnel capability is decided by a
// real, reversible interface-creation attempt (see detectTunnel) rather than by
// the presence of an external client binary. Tests inject these functions to
// cover hosts with and without working WireGuard.
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

	tunnelStatus, tunnelReason := detectTunnel(probes)
	result.Tunnel = tunnelStatus
	if tunnelReason != "" {
		reasons = append(reasons, tunnelReason)
	}
	result.Reason = strings.Join(dedupe(reasons), " / ")
	return result
}

// detectTunnel 判断本机能否真正承载 mesh 隧道，并给出**如实**的原因。
//
// 历史教训（本 bug 的根因）：旧实现只看外部「隧道/代理客户端」二进制
// （neilico-tunnel/npc）是否存在，于是当宿主机明明能创建内核 WireGuard 接口
// （CAP_NET_ADMIN + /dev/net/tun + 内核 WG 支持）时，仍把 tunnel 报成
// unavailable，reason 还写着「未检测到可用的隧道/代理客户端」。这个错误的
// self-report 会一路传到控制面，把「mesh 起不来」掩盖成「客户端没装」，
// 并导致 agent 自己放弃应用 mesh 配置（宿主上永远没有 wg0）。
//
// 判定顺序：
//  1. 真实尝试：创建（再删除）一个一次性 WireGuard 接口。成功即 ready——这是
//     唯一能证明「本机真的能建隧道」的证据，也是应用配置前的最后一道门禁。
//  2. 没有接线真实探测时（测试/非 Linux）退化为静态判断：内核/系统 WireGuard
//     组件 + CAP_NET_ADMIN + /dev/net/tun，或存在外部隧道客户端。
func detectTunnel(probes Probes) (status string, reason string) {
	if probes.CanCreateInterface != nil {
		if err := probes.CanCreateInterface(); err != nil {
			return nodecapabilities.Unavailable, "无法创建 WireGuard 接口：" + err.Error()
		}
		return nodecapabilities.Ready, ""
	}
	switch probes.GOOS {
	case "linux":
		var missing []string
		if !kernelWireGuardSupported(probes) {
			missing = append(missing, "内核未提供 WireGuard 支持")
		}
		if !probes.PathExists("/dev/net/tun") {
			missing = append(missing, "无 /dev/net/tun")
		}
		if !probes.HasAdmin() {
			missing = append(missing, "缺 CAP_NET_ADMIN")
		}
		if !commandAvailable(probes.LookPath, "ip") {
			missing = append(missing, "缺 ip 工具")
		}
		if len(missing) > 0 {
			return nodecapabilities.Unavailable, "无法创建 WireGuard 接口（" + strings.Join(missing, " / ") + "）"
		}
		return nodecapabilities.Ready, ""
	default:
		if probes.TunnelClient() {
			return nodecapabilities.Ready, ""
		}
		return nodecapabilities.Unavailable, "未检测到可用的隧道/代理客户端"
	}
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

// kernelWireGuardSupported 报告内核/系统层面是否具备 WireGuard 能力。
// 注意：`/sys/module/wireguard` 只在模块被加载时存在，内建（built-in）内核
// 没有该目录，因此这里只把它当作「之一」的信号；真正的权威判断是真实建接口。
func kernelWireGuardSupported(probes Probes) bool {
	if probes.PathExists("/sys/module/wireguard") {
		return true
	}
	return wireGuardComponent(probes)
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
