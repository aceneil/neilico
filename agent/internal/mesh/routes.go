package mesh

import (
	"strings"

	"neilico/agent/internal/route"
)

// PeerRouteCommands 返回"把各对端的 AllowedIPs 指向隧道接口"的路由命令。
//
// 为什么必须显式加：WireGuard 的 AllowedIPs 只决定"这个包该发给哪个 peer"
// （加密路由），**不会**写入内核主路由表——这正是 wg-quick 在 setconf 之后
// 还要逐条执行 `ip route add <AllowedIPs> dev <iface>` 的原因。
//
// 少了这一步的表现（真机实测）：双方握手成功、收发计数在涨，但**数据一个字节
// 都过不去**——`ip route get 100.64.0.2` 显示包被送去局域网网关而不是 wg0。
// 子网路由（把对端背后的网段接进虚拟网）同样依赖这些路由，否则只有握手没有转发。
//
// 用 `route replace`：可重复执行（幂等），也不会因为路由已存在而失败。
func PeerRouteCommands(config Config) []route.Command {
	if len(config.Peers) == 0 || strings.TrimSpace(config.Interface) == "" {
		return nil
	}
	commands := make([]route.Command, 0, len(config.Peers))
	seen := make(map[string]struct{}, len(config.Peers))
	for _, peer := range config.Peers {
		for _, allowed := range peer.AllowedIPs {
			cidr := strings.TrimSpace(allowed)
			if cidr == "" {
				continue
			}
			if _, duplicate := seen[cidr]; duplicate {
				continue
			}
			seen[cidr] = struct{}{}
			commands = append(commands, route.Command{
				Name:    "ip",
				Args:    []string{"route", "replace", cidr, "dev", config.Interface},
				Comment: "对端 " + shortKey(peer.PublicKey) + " 的 AllowedIPs",
			})
		}
	}
	return commands
}
