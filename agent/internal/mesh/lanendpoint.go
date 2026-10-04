package mesh

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

const defaultListenPort = 51820

// meshRange 是 NEILICO 虚拟 IP 段（CGNAT 100.64.0.0/10）。
// 它绝不能当内网地址：否则会把"对端的虚拟 IP"当成同内网地址去拨，永远拨不通。
var meshRange = netip.MustParsePrefix("100.64.0.0/10")

// skippedInterfacePrefixes 是容器/虚拟网桥的接口名前缀。实测宿主机上会有 10 个左右
// docker/br- 网桥，它们既不是内网也不可达对端，还会把上报条数配额吃光。
var skippedInterfacePrefixes = []string{"docker", "br-", "veth", "virbr", "tun", "tap", "tailscale", "lo"}

// LocalPrefixes 返回本机的内网地址前缀（CIDR），用于判断"对端是否与本机同一内网"。
//
// excludeInterfaces 用于排除 NEILICO 自己的隧道接口（如 wg0）。
// 过滤规则：跳过回环/链路本地/CGNAT（虚拟 IP 段）、跳过容器网桥类接口。
func LocalPrefixes(excludeInterfaces ...string) []netip.Prefix {
	excluded := make(map[string]struct{}, len(excludeInterfaces))
	for _, name := range excludeInterfaces {
		excluded[strings.TrimSpace(name)] = struct{}{}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	prefixes := make([]netip.Prefix, 0, 4)
	seen := make(map[string]struct{})
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if _, skip := excluded[iface.Name]; skip {
			continue
		}
		if isVirtualInterface(iface.Name) {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				// 有些平台上报裸 IP（无掩码）：按 /32 处理
				if parsed, ipErr := netip.ParseAddr(address.String()); ipErr == nil {
					prefix = netip.PrefixFrom(parsed, parsed.BitLen())
				} else {
					continue
				}
			}
			ip := prefix.Addr()
			if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			if meshRange.Contains(ip) {
				continue
			}
			key := prefix.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			prefixes = append(prefixes, prefix)
		}
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].String() < prefixes[j].String() })
	return prefixes
}

// isVirtualInterface 判断是否是容器网桥/虚拟接口（按接口名前缀）。
func isVirtualInterface(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range skippedInterfacePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// PreferLANEndpoints 把"与本机处于同一内网"的对端 endpoint 换成内网地址。
//
// 背景：agent 上报的公网 endpoint 在 NAT/代理出口后常常互相拨不通——实测中一台设备
// 的出口是云代理 IP（收不到入站 UDP），另一台根本探测不到公网地址，于是双方都无法
// 主动建连、隧道永远起不来。而同内网的设备用内网地址是直连可达的。
//
// 只对"对端内网地址落在本机某个内网网段内"的对端生效；跨网络的对端保持原公网地址不变。
// 返回替换说明（供日志与测试断言）。
func PreferLANEndpoints(config *Config, prefixes []netip.Prefix) []string {
	if config == nil || len(prefixes) == 0 || len(config.Peers) == 0 {
		return nil
	}
	port := config.ListenPort
	if port <= 0 {
		port = defaultListenPort
	}
	notes := make([]string, 0, len(config.Peers))
	for index := range config.Peers {
		peer := &config.Peers[index]
		lan := firstAddressInSubnet(peer.LocalAddresses, prefixes)
		if lan == "" {
			continue
		}
		peerPort := port
		if peer.ListenPort > 0 {
			// 对端的端口要用**对端自己**的监听端口（各节点可能配置不同）
			peerPort = peer.ListenPort
		}
		endpoint := net.JoinHostPort(lan, strconv.Itoa(peerPort))
		if peer.Endpoint == endpoint {
			continue
		}
		notes = append(notes, fmt.Sprintf("对端 %s 与本机处于同一内网，改用内网地址 %s（原地址 %s）",
			shortKey(peer.PublicKey), endpoint, orNone(peer.Endpoint)))
		config.WireGuardConfig = rewritePeerEndpoint(config.WireGuardConfig, peer.PublicKey, endpoint)
		peer.Endpoint = endpoint
	}
	return notes
}

// rewritePeerEndpoint 在渲染好的 WireGuard 配置文本里，把指定对端的 Endpoint 换成新值
// （原有 Endpoint 行就地替换；没有则在 PublicKey 后插入一行）。
func rewritePeerEndpoint(text, publicKey, endpoint string) string {
	if strings.TrimSpace(text) == "" || strings.TrimSpace(publicKey) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	currentPeer := false
	insertAt := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(trimmed, "[Peer]"):
			currentPeer = false
			insertAt = -1
		case !currentPeer && strings.HasPrefix(trimmed, "PublicKey") && strings.Contains(trimmed, publicKey):
			currentPeer = true
			insertAt = index + 1
		case currentPeer && strings.HasPrefix(trimmed, "Endpoint"):
			lines[index] = "Endpoint = " + endpoint
			return strings.Join(lines, "\n")
		case currentPeer && strings.HasPrefix(trimmed, "["):
			// 走到下一个段落还没找到 Endpoint：在 PublicKey 之后补一行
			if insertAt > 0 {
				lines = append(lines[:insertAt], append([]string{"Endpoint = " + endpoint}, lines[insertAt:]...)...)
				return strings.Join(lines, "\n")
			}
			return strings.Join(lines, "\n")
		}
	}
	if currentPeer && insertAt > 0 {
		lines = append(lines[:insertAt], append([]string{"Endpoint = " + endpoint}, lines[insertAt:]...)...)
	}
	return strings.Join(lines, "\n")
}

func firstAddressInSubnet(addresses []string, prefixes []netip.Prefix) string {
	for _, address := range addresses {
		candidate := strings.TrimSpace(address)
		if candidate == "" {
			continue
		}
		ip, err := parseAddress(candidate)
		if err != nil {
			continue
		}
		for _, prefix := range prefixes {
			if prefix.Contains(ip) {
				return ip.String()
			}
		}
	}
	return ""
}

func parseAddress(value string) (netip.Addr, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Addr(), nil
	}
	return netip.ParseAddr(value)
}

func shortKey(publicKey string) string {
	trimmed := strings.TrimSpace(publicKey)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12] + "…"
}

func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(未上报)"
	}
	return value
}
