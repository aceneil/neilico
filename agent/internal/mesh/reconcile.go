package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"neilico/agent/internal/client"
	"neilico/agent/internal/route"
	"neilico/agent/internal/state"
)

type ApplyObserver interface {
	Apply(string)
	SetConfigVersion(int)
	SetPeers(int)
	SetProxyPending(bool)
}

type RouteManager interface {
	Apply(context.Context, []route.Route) ([]route.Command, error)
	Plan([]route.Route) ([]route.Command, error)
	Cleanup(context.Context) error
}

type Reconciler struct {
	statePath       string
	applier         Applier
	routes          RouteManager
	probe           DriftProbe
	metrics         ApplyObserver
	logger          *slog.Logger
	output          io.Writer
	dryRun          bool
	allowProxy      bool
	allowForwarding bool
	meshOptions     struct {
		Interface  string
		MTU        int
		ListenPort int
	}
}

type ReconcilerOptions struct {
	StatePath       string
	Applier         Applier
	Routes          RouteManager
	Probe           DriftProbe
	Metrics         ApplyObserver
	Logger          *slog.Logger
	Output          io.Writer
	DryRun          bool
	AllowProxy      bool
	AllowForwarding bool
	Interface       string
	MTU             int
	ListenPort      int
}

func NewReconciler(options ReconcilerOptions) *Reconciler {
	output := options.Output
	if output == nil {
		output = os.Stdout
	}
	reconciler := &Reconciler{
		statePath:       options.StatePath,
		applier:         options.Applier,
		routes:          options.Routes,
		probe:           options.Probe,
		metrics:         options.Metrics,
		logger:          options.Logger,
		output:          output,
		dryRun:          options.DryRun,
		allowProxy:      options.AllowProxy,
		allowForwarding: options.AllowForwarding,
	}
	reconciler.meshOptions.Interface = options.Interface
	reconciler.meshOptions.MTU = options.MTU
	reconciler.meshOptions.ListenPort = options.ListenPort
	return reconciler
}

func (r *Reconciler) Reconcile(ctx context.Context, delivery client.Delivery) error {
	current, _, err := state.Load(r.statePath)
	if err != nil {
		return err
	}
	hash, err := ConfigHash(delivery)
	if err != nil {
		return err
	}

	// 先核对本地实物，再决定是否短路。
	//
	// 现场铁证：容器被 recreate 后内核 netns 被清空（wg0 与对端路由凭空消失，
	// `ip -brief addr show wg0` 为空），但 state.json 的 applied_version / applied_config_hash
	// 都原封不动。若这里只比哈希就跳过，已消失的 Mesh 永远不会被重建
	// （真机踩到：重启两端 agent 也无效，走的都是这条短路）。
	drift, driftErr := r.detectDrift(ctx, r.expectedProbeConfig(delivery))
	if driftErr != nil {
		// 探测失败 ≠ 漂移：宁可保守跳过，也不要因探测工具缺失而反复重建接口。
		if r.logger != nil {
			r.logger.Warn("local mesh state probe failed; assuming no drift", "error", driftErr)
		}
	}
	if len(drift) > 0 && r.logger != nil {
		r.logger.Info("local mesh state drift detected; re-applying", "missing", strings.Join(drift, "; "))
	}

	if current.AppliedConfigHash == hash && len(drift) == 0 {
		current.AppliedVersion = delivery.Version
		current.ApplicationSchema = ApplicationSchemaVersion
		current.AppliedPeers = appliedPeers(delivery)
		if err := state.Save(r.statePath, current); err != nil {
			return err
		}
		if r.metrics != nil {
			r.metrics.Apply("unchanged")
			r.metrics.SetConfigVersion(delivery.Version)
			r.metrics.SetPeers(peerCount(delivery))
		}
		return nil
	}

	secret := ""
	if delivery.Network != nil {
		secret = delivery.Network.NetworkSecret
	}
	config := Config{
		Version:         delivery.Version,
		NodeID:          delivery.Node.ID,
		WireGuardConfig: delivery.WireGuardConfig,
		Interface:       r.meshOptions.Interface,
		MTU:             r.meshOptions.MTU,
		ListenPort:      r.meshOptions.ListenPort,
		Peers:           peerList(delivery),
		Routes:          delivery.Routes,
		NetworkSecret:   secret,
	}
	// 同内网的对端改用内网地址（公网出口常常不可达，见 PreferLANEndpoints 注释）
	for _, note := range PreferLANEndpoints(&config, LocalPrefixes(r.meshOptions.Interface)) {
		if r.logger != nil {
			r.logger.Info("mesh endpoint preference", "detail", note)
		}
	}
	if err := r.applier.Apply(ctx, config); err != nil {
		if r.metrics != nil {
			r.metrics.Apply("failure")
		}
		return fmt.Errorf("apply WireGuard configuration: %w", err)
	}

	if delivery.Network != nil {
		if setter, ok := r.routes.(interface{ SetMeshCIDR(string) }); ok && strings.TrimSpace(delivery.Network.CIDR) != "" {
			setter.SetMeshCIDR(delivery.Network.CIDR)
		}
	}
	localRoutes := LocalRoutes(delivery.Node.ID, delivery.Routes)
	if len(localRoutes) > 0 && r.allowForwarding && r.logger != nil {
		r.logger.Info("forwarding enabled; system items will be changed", "sysctl", "net.ipv4.ip_forward=1", "iptables", "MASQUERADE")
	}
	if r.dryRun {
		commands, err := r.routes.Plan(localRoutes)
		if err != nil {
			if r.metrics != nil {
				r.metrics.Apply("failure")
			}
			return fmt.Errorf("plan routes: %w", err)
		}
		fmt.Fprintln(r.output, "=== route and forwarding commands ===")
		for _, command := range commands {
			fmt.Fprintln(r.output, command.String())
		}
	} else if _, err := r.routes.Apply(ctx, localRoutes); err != nil {
		if r.metrics != nil {
			r.metrics.Apply("failure")
		}
		return fmt.Errorf("apply routes: %w", err)
	}

	if len(delivery.ProxyRules) > 0 && r.logger != nil {
		r.logger.Info("proxy rules recorded; NPS tunnel client pending integration", "rules", len(delivery.ProxyRules))
	}
	if r.metrics != nil {
		r.metrics.SetProxyPending(r.allowProxy)
	}

	current.AppliedVersion = delivery.Version
	current.AppliedConfigHash = hash
	current.ApplicationSchema = ApplicationSchemaVersion
	current.NetworkSecret = secret
	current.AppliedPeers = appliedPeers(delivery)
	if parsed, parseErr := parseWireGuardConfig(delivery.WireGuardConfig); parseErr == nil && parsed.privateKey != "" {
		current.PrivateKey = parsed.privateKey
	}
	if err := state.Save(r.statePath, current); err != nil {
		if r.metrics != nil {
			r.metrics.Apply("failure")
		}
		return err
	}
	if r.metrics != nil {
		r.metrics.Apply("success")
		r.metrics.SetConfigVersion(delivery.Version)
		r.metrics.SetPeers(peerCount(delivery))
	}
	return nil
}

func (r *Reconciler) Cleanup(ctx context.Context, cleanupMesh bool) error {
	var failures []string
	if r.routes != nil {
		if err := r.routes.Cleanup(ctx); err != nil {
			failures = append(failures, "routes: "+err.Error())
		}
	}
	if r.applier != nil && cleanupMesh {
		if err := r.applier.Cleanup(ctx); err != nil {
			failures = append(failures, "WireGuard: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(joinErrors(failures))
	}
	return nil
}

// expectedProbeConfig 从投递配置抽取"漂移探测"需要的期望项：
// 接口名、各对端公钥与 AllowedIPs（路由期望由 AllowedIPs 推导，见 PeerRouteCommands）。
func (r *Reconciler) expectedProbeConfig(delivery client.Delivery) Config {
	return Config{
		Interface: r.meshOptions.Interface,
		Peers:     peerList(delivery),
	}
}

// detectDrift 调用注入的探测器核对本地实物；dry-run 或未配置探测器时视为无漂移。
func (r *Reconciler) detectDrift(ctx context.Context, expected Config) ([]string, error) {
	if r.probe == nil || r.dryRun {
		return nil, nil
	}
	return r.probe.Missing(ctx, expected)
}

// LocalDrift 校对 state.json 声称"已应用"的本地隧道是否真实存在。
//
// 这是容器重启场景的兜底：agent 启动后先在这里核对一遍——只要 state 声称应用过
// （有 applied_version 或 applied_config_hash），就把上次成功应用时落盘的期望状态
// （applied_peers）交给探测器比对。返回非空的缺失项即代表本地实物已漂移，
// 调用方必须**无条件强制重新应用**（不得因版本号/哈希未变而短路）。
//
// 返回空切片表示无需干预；error 表示探测本身不确定（不应当作漂移）。
func (r *Reconciler) LocalDrift(ctx context.Context) ([]string, error) {
	if r.probe == nil || r.dryRun {
		return nil, nil
	}
	current, exists, err := state.Load(r.statePath)
	if err != nil {
		return nil, err
	}
	if !exists || (current.AppliedVersion == 0 && strings.TrimSpace(current.AppliedConfigHash) == "") {
		// 从未应用过：交给正常的首次对账，没必要在这里“发现漂移”。
		return nil, nil
	}
	return r.probe.Missing(ctx, Config{
		Interface: r.meshOptions.Interface,
		Peers:     clientPeersFromState(current.AppliedPeers),
	})
}

// appliedPeers 把投递里的对端（仅公开信息）转成可落盘的期望状态。
func appliedPeers(delivery client.Delivery) []state.AppliedPeer {
	peers := peerList(delivery)
	if len(peers) == 0 {
		return nil
	}
	result := make([]state.AppliedPeer, 0, len(peers))
	for _, peer := range peers {
		result = append(result, state.AppliedPeer{
			PublicKey:  peer.PublicKey,
			AllowedIPs: append([]string(nil), peer.AllowedIPs...),
		})
	}
	return result
}

// clientPeersFromState 把落盘的期望对端还原成探测用的对端列表。
func clientPeersFromState(peers []state.AppliedPeer) []client.Peer {
	if len(peers) == 0 {
		return nil
	}
	result := make([]client.Peer, 0, len(peers))
	for _, peer := range peers {
		result = append(result, client.Peer{
			PublicKey:  peer.PublicKey,
			AllowedIPs: append([]string(nil), peer.AllowedIPs...),
		})
	}
	return result
}

// ApplicationSchemaVersion 标识"本地应用逻辑"的版本，参与配置哈希。
//
// 为什么需要它：哈希一致时 agent 会跳过应用（省事、幂等）。但**改动了 applier 的行为**
// （例如新增"为对端 AllowedIPs 加路由"）后，同一份控制面配置对应的本地动作变了，
// 若哈希不变，已升级的 agent 会一直跳过应用，新动作永远装不上——真机踩到：
// 升级到带路由修复的镜像后 wg0 上依然一条路由都没有，直到配置发生变化才生效。
//
// 规则：凡是修改 applier/本地应用行为，必须把这个常量 +1。
//
// v4：应用时新增落盘"期望的本地状态"（state.applied_peers），并在启动/对账时据此
// 校对本地实物（wg0、对端、路由）是否还在。升级后必须重新应用一次，才能把期望状态
// 补进旧 state.json，之后的漂移检测才有完整依据。
const ApplicationSchemaVersion = 4

func ConfigHash(delivery client.Delivery) (string, error) {
	comparable := delivery
	comparable.Version = 0
	encoded, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		Delivery      client.Delivery `json:"delivery"`
	}{SchemaVersion: ApplicationSchemaVersion, Delivery: comparable})
	if err != nil {
		return "", fmt.Errorf("hash configuration: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func peerCount(delivery client.Delivery) int {
	if delivery.Network == nil {
		return 0
	}
	return len(delivery.Network.Peers)
}

func joinErrors(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += "; "
		}
		result += value
	}
	return result
}

func peerList(delivery client.Delivery) []client.Peer {
	if delivery.Network == nil {
		return nil
	}
	return delivery.Network.Peers
}
