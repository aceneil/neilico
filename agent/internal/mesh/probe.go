package mesh

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"

	"neilico/agent/internal/client"
	"neilico/agent/internal/route"
)

// DriftProbe 探测"期望的本地隧道状态"是否真实存在于本机。
//
// 为什么需要这个抽象：state.json 里的 applied_version / applied_config_hash 只能证明
// "agent 当初成功应用过"，**不能证明实物还在**。真机实测的病历：容器被 recreate 后
// 内核 netns 被清空，wg0 与对端路由凭空消失（`ip -brief addr show wg0` 为空），
// 而 state.json 原封不动（applied_version=1155、application_schema=3）。
// 此时 agent 启动后拉配置拿到 304 NotModified、再因"哈希未变、自认为已应用"跳过重建，
// Mesh 永远回不来。控制器侧（network_members/VIP）完全正常，问题只在本地实物校对缺失。
//
// 抽象成接口是为了单测能注入假实现，不依赖 root 权限或真实内核网络。
type DriftProbe interface {
	// Missing 返回期望状态中缺失/不一致项的简短描述（如 "interface wg0 missing"）。
	// 返回空切片表示本地实物与期望一致。
	// 返回 error 表示探测本身不确定（工具缺失等）——调用方应按"无漂移"处理，
	// 避免因探测器故障而反复重建接口。
	Missing(ctx context.Context, config Config) ([]string, error)
}

// SystemProbe 用 iproute2 / wireguard-tools 探测真实内核状态。
type SystemProbe struct {
	executor route.Executor
}

func NewSystemProbe(executor route.Executor) *SystemProbe {
	if executor == nil {
		executor = route.ExecExecutor{}
	}
	return &SystemProbe{executor: executor}
}

var _ DriftProbe = (*SystemProbe)(nil)

// Missing 依次核对：接口存在 → 对端 peer 配置一致 → 对端路由存在。
func (p *SystemProbe) Missing(ctx context.Context, config Config) ([]string, error) {
	iface := strings.TrimSpace(config.Interface)
	if iface == "" {
		return nil, nil
	}
	exists, err := interfaceExists(ctx, p.executor, iface)
	if err != nil {
		return nil, err
	}
	if !exists {
		// wg0 整个不见了 = 最典型的漂移（容器重启清空 netns）。接口都没了，
		// 后续 peer/路由检查无从谈起，直接短路。
		return []string{"interface " + iface + " missing"}, nil
	}
	var missing []string
	peers, err := p.missingPeers(ctx, iface, config.Peers)
	if err != nil {
		return nil, err
	}
	missing = append(missing, peers...)
	routes, err := p.missingPeerRoutes(ctx, iface, config)
	if err != nil {
		return nil, err
	}
	missing = append(missing, routes...)
	return missing, nil
}

// missingPeers 用 `wg show <iface> dump` 核对每个期望对端是否存在、AllowedIPs 是否一致。
func (p *SystemProbe) missingPeers(ctx context.Context, iface string, peers []client.Peer) ([]string, error) {
	if len(peers) == 0 {
		return nil, nil
	}
	output, err := p.executor.Output(ctx, route.Command{Name: "wg", Args: []string{"show", iface, "dump"}})
	if err != nil {
		return nil, fmt.Errorf("probe WireGuard peers on %s: %w", iface, err)
	}
	actual := make(map[string]map[string]struct{}, len(peers))
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		// `wg show <iface> dump`：接口行 4 个字段（private/public/listen-port/fwmark），
		// 对端行 8 个字段。只解析对端行。
		if len(fields) < 8 {
			continue
		}
		key := strings.TrimSpace(fields[0])
		actual[key] = normalizeAllowedIPs(strings.Split(fields[3], ","))
	}
	var missing []string
	for _, peer := range peers {
		key := strings.TrimSpace(peer.PublicKey)
		if key == "" {
			continue
		}
		allowed, ok := actual[key]
		if !ok {
			missing = append(missing, "peer "+shortKey(key)+" missing")
			continue
		}
		for cidr := range normalizeAllowedIPs(peer.AllowedIPs) {
			if _, ok := allowed[cidr]; !ok {
				missing = append(missing, "peer "+shortKey(key)+" allowed_ips "+cidr+" missing")
			}
		}
	}
	return missing, nil
}

// missingPeerRoutes 核对每个对端 AllowedIPs → 隧道接口的路由是否真的存在。
func (p *SystemProbe) missingPeerRoutes(ctx context.Context, iface string, config Config) ([]string, error) {
	commands := PeerRouteCommands(config)
	if len(commands) == 0 {
		return nil, nil
	}
	var missing []string
	for _, command := range commands {
		if len(command.Args) < 3 {
			continue
		}
		cidr := command.Args[2]
		present, err := p.routeExists(ctx, cidr, iface)
		if err != nil {
			return nil, err
		}
		if !present {
			missing = append(missing, "route "+cidr+" dev "+iface+" missing")
		}
	}
	return missing, nil
}

// routeExists 判断指定 CIDR 是否已经路由到指定接口。
func (p *SystemProbe) routeExists(ctx context.Context, cidr, iface string) (bool, error) {
	output, err := p.executor.Output(ctx, route.Command{Name: "ip", Args: []string{"route", "show", cidr, "dev", iface}})
	if err != nil {
		var exitErr *exec.ExitError
		// `ip route show` 在找不到时可能以退出码 1 结束（与 route.Manager 的约定一致）。
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

// normalizeAllowedIPs 把 CIDR/裸 IP 归一化，避免 /32 与裸 IP、"+" 之类的书写差异造成误报。
func normalizeAllowedIPs(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(trimmed); err == nil {
			result[prefix.Masked().String()] = struct{}{}
			continue
		}
		if addr, err := netip.ParseAddr(trimmed); err == nil {
			result[netip.PrefixFrom(addr, addr.BitLen()).String()] = struct{}{}
			continue
		}
		result[trimmed] = struct{}{}
	}
	return result
}
