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
	if current.AppliedConfigHash == hash {
		current.AppliedVersion = delivery.Version
		current.ApplicationSchema = ApplicationSchemaVersion
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

// ApplicationSchemaVersion 标识"本地应用逻辑"的版本，参与配置哈希。
//
// 为什么需要它：哈希一致时 agent 会跳过应用（省事、幂等）。但**改动了 applier 的行为**
// （例如新增"为对端 AllowedIPs 加路由"）后，同一份控制面配置对应的本地动作变了，
// 若哈希不变，已升级的 agent 会一直跳过应用，新动作永远装不上——真机踩到：
// 升级到带路由修复的镜像后 wg0 上依然一条路由都没有，直到配置发生变化才生效。
//
// 规则：凡是修改 applier/本地应用行为，必须把这个常量 +1。
const ApplicationSchemaVersion = 3

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
