package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
)

// 「远程桌面」设备授权策略的枚举取值（与 desktop/docs/api-contract.md 一致）。
const (
	TunnelModeAuto   = "auto"
	TunnelModeDirect = "direct"
	TunnelModeRelay  = "relay"
)

// 只读的子网路由状态。
const (
	SubnetRoutesReady       = "ready"
	SubnetRoutesDegraded    = "degraded"
	SubnetRoutesUnavailable = "unavailable"
)

// remoteDesktopIsolatedTargetPort 是单独隧道转发到的节点端口：RustDesk 的「直连 IP」端口。
// 单独隧道把宿主上发布的一个端口转发到该节点的这个端口，从而绕过 hbbr 中继直达设备。
const remoteDesktopIsolatedTargetPort = 21118

// RemoteDesktopPolicyPatchInput 是 PATCH 的**局部更新**入参。
// 用指针区分「未提供该字段」与「显式置零/置空」：只有非 nil 的字段会被改动。
type RemoteDesktopPolicyPatchInput struct {
	RemoteControlAllowed  *bool      `json:"remote_control_allowed"`
	TunnelMode            *string    `json:"tunnel_mode"`
	IsolatedTunnelEnabled *bool      `json:"isolated_tunnel_enabled"`
	MeshJoined            *bool      `json:"mesh_joined"`
	MeshNetworkID         *uuid.UUID `json:"mesh_network_id,omitempty"`
}

// RemoteDesktopIsolatedTunnelView 描述「单独隧道」：复用现有 StreamRule 的能力。
type RemoteDesktopIsolatedTunnelView struct {
	Enabled      bool       `json:"enabled"`
	StreamRuleID *uuid.UUID `json:"stream_rule_id"`
}

// RemoteDesktopMeshView 描述 Mesh 成员身份（读自现有 NetworkMember）。
type RemoteDesktopMeshView struct {
	Joined    bool       `json:"joined"`
	NetworkID *uuid.UUID `json:"network_id"`
	VirtualIP *string    `json:"virtual_ip"`
}

// RemoteDesktopReadonlyView 是只读派生字段。
type RemoteDesktopReadonlyView struct {
	SubnetRoutes string `json:"subnet_routes"`
}

// RemoteDesktopDevicePolicyView 是 GET/PATCH 返回的单台设备授权视图。
type RemoteDesktopDevicePolicyView struct {
	NodeID               uuid.UUID                       `json:"node_id"`
	RemoteControlAllowed bool                            `json:"remote_control_allowed"`
	TunnelMode           string                          `json:"tunnel_mode"`
	IsolatedTunnel       RemoteDesktopIsolatedTunnelView `json:"isolated_tunnel"`
	Mesh                 RemoteDesktopMeshView           `json:"mesh"`
	Readonly             RemoteDesktopReadonlyView       `json:"readonly"`
}

// RemoteDesktopDevicePolicyList 是设备授权列表（形状与 /devices 一致：items/total）。
type RemoteDesktopDevicePolicyList struct {
	Items []RemoteDesktopDevicePolicyView `json:"items"`
	Total int64                           `json:"total"`
}

// RemoteDesktopPolicyService 管理每台设备的远控授权开关。
//
// 它**只做策略**：单独隧道复用 StreamRuleService，Mesh 复用 NetworkService，
// 不新造转发引擎或成员能力。真实持久化在 remote_desktop_device_policies 表。
//
// ⚠️ 事务注意：SQLite 在本部署里 SetMaxOpenConns(1)，一旦开事务就只能用事务句柄。
// 因此这里刻意**不**把跨服务调用（StreamRule/Network）包进本服务自己的事务里，
// 避免 s.db 在新连接上等待同一把锁而互锁挂死。
type RemoteDesktopPolicyService struct {
	db       *gorm.DB
	streams  *StreamRuleService
	networks *NetworkService
}

func NewRemoteDesktopPolicyService(db *gorm.DB, streams *StreamRuleService, networks *NetworkService) *RemoteDesktopPolicyService {
	return &RemoteDesktopPolicyService{db: db, streams: streams, networks: networks}
}

type nodeMeshState struct {
	NetworkID uuid.UUID
	VirtualIP string
}

// List 返回指定租户范围内每台设备的授权状态。未建过策略的节点返回默认值（不报错）。
func (s *RemoteDesktopPolicyService) List(ctx context.Context, tenantID *uuid.UUID) (RemoteDesktopDevicePolicyList, error) {
	query := s.db.WithContext(ctx).Model(&models.Node{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var nodes []models.Node
	if err := query.Order("created_at DESC").Find(&nodes).Error; err != nil {
		return RemoteDesktopDevicePolicyList{}, fmt.Errorf("list nodes for policies: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	policies, err := s.loadPolicies(ctx, ids)
	if err != nil {
		return RemoteDesktopDevicePolicyList{}, err
	}
	mesh, err := s.loadMeshStates(ctx, ids)
	if err != nil {
		return RemoteDesktopDevicePolicyList{}, err
	}
	routes, err := s.loadSubnetRouteStatus(ctx, ids)
	if err != nil {
		return RemoteDesktopDevicePolicyList{}, err
	}
	items := make([]RemoteDesktopDevicePolicyView, 0, len(nodes))
	for _, node := range nodes {
		meshState, joined := mesh[node.ID]
		items = append(items, buildPolicyView(node.ID, policies[node.ID], meshState, joined, routes[node.ID]))
	}
	return RemoteDesktopDevicePolicyList{Items: items, Total: int64(len(items))}, nil
}

// Patch 局部更新某台设备的授权开关。只改动入参里非 nil 的字段。
func (s *RemoteDesktopPolicyService) Patch(
	ctx context.Context,
	nodeID uuid.UUID,
	tenantID *uuid.UUID,
	input RemoteDesktopPolicyPatchInput,
) (RemoteDesktopDevicePolicyView, error) {
	node, err := s.getNode(ctx, nodeID, tenantID)
	if err != nil {
		return RemoteDesktopDevicePolicyView{}, err
	}
	policy, exists, err := s.loadPolicy(ctx, nodeID)
	if err != nil {
		return RemoteDesktopDevicePolicyView{}, err
	}
	policy.NodeID = nodeID
	policy.TenantID = node.TenantID

	// 先做纯校验：非法枚举值立即 400，不留任何副作用。
	if input.TunnelMode != nil {
		mode, err := normalizeTunnelMode(*input.TunnelMode)
		if err != nil {
			return RemoteDesktopDevicePolicyView{}, err
		}
		policy.TunnelMode = mode
	}
	if strings.TrimSpace(policy.TunnelMode) == "" {
		policy.TunnelMode = TunnelModeAuto
	}
	if input.RemoteControlAllowed != nil {
		policy.RemoteControlAllowed = *input.RemoteControlAllowed
	}

	// 预解析前置条件：把可能失败的检查放在任何写入之前，避免留下半成品。
	var joinNetwork *uuid.UUID
	if input.MeshJoined != nil && *input.MeshJoined {
		member, err := s.isMember(ctx, nodeID)
		if err != nil {
			return RemoteDesktopDevicePolicyView{}, err
		}
		if !member {
			target, err := s.resolveJoinNetwork(ctx, node, input.MeshNetworkID)
			if err != nil {
				return RemoteDesktopDevicePolicyView{}, err
			}
			joinNetwork = &target
		}
	}
	if input.IsolatedTunnelEnabled != nil && *input.IsolatedTunnelEnabled && !policy.IsolatedTunnelEnabled {
		member, err := s.isMember(ctx, nodeID)
		if err != nil {
			return RemoteDesktopDevicePolicyView{}, err
		}
		if !member {
			return RemoteDesktopDevicePolicyView{}, fmt.Errorf("%w: 设备未分配虚拟 IP，无法建立单独隧道", ErrInvalidInput)
		}
	}

	// Mesh 加入/退出：复用现有成员能力。
	if input.MeshJoined != nil {
		if *input.MeshJoined {
			if joinNetwork != nil {
				if _, err := s.networks.AddMember(ctx, *joinNetwork, &node.TenantID, NetworkMemberInput{NodeID: nodeID}); err != nil {
					return RemoteDesktopDevicePolicyView{}, err
				}
			}
		} else if err := s.leaveAllNetworks(ctx, node, nodeID); err != nil {
			return RemoteDesktopDevicePolicyView{}, err
		}
	}

	// 单独隧道：复用现有 StreamRule 能力（创建/删除一条该节点专属的转发规则）。
	var createdRuleID *uuid.UUID
	if input.IsolatedTunnelEnabled != nil {
		want := *input.IsolatedTunnelEnabled
		switch {
		case want && !policy.IsolatedTunnelEnabled:
			rule, err := s.createIsolatedRule(ctx, node)
			if err != nil {
				return RemoteDesktopDevicePolicyView{}, err
			}
			policy.IsolatedTunnelEnabled = true
			policy.IsolatedStreamRuleID = &rule.ID
			createdRuleID = &rule.ID
		case !want && policy.IsolatedTunnelEnabled:
			if policy.IsolatedStreamRuleID != nil {
				if err := s.streams.Delete(ctx, *policy.IsolatedStreamRuleID, &node.TenantID); err != nil {
					return RemoteDesktopDevicePolicyView{}, err
				}
			}
			policy.IsolatedTunnelEnabled = false
			policy.IsolatedStreamRuleID = nil
		}
	}

	if err := s.savePolicy(ctx, &policy, exists); err != nil {
		// 补偿：策略没落库就别留下孤儿转发规则。
		if createdRuleID != nil {
			_ = s.streams.Delete(ctx, *createdRuleID, &node.TenantID)
		}
		return RemoteDesktopDevicePolicyView{}, err
	}
	return s.viewForNode(ctx, node, policy)
}

func (s *RemoteDesktopPolicyService) viewForNode(
	ctx context.Context,
	node models.Node,
	policy models.RemoteDesktopDevicePolicy,
) (RemoteDesktopDevicePolicyView, error) {
	mesh, err := s.loadMeshStates(ctx, []uuid.UUID{node.ID})
	if err != nil {
		return RemoteDesktopDevicePolicyView{}, err
	}
	routes, err := s.loadSubnetRouteStatus(ctx, []uuid.UUID{node.ID})
	if err != nil {
		return RemoteDesktopDevicePolicyView{}, err
	}
	meshState, joined := mesh[node.ID]
	return buildPolicyView(node.ID, policy, meshState, joined, routes[node.ID]), nil
}

// createIsolatedRule 复用 StreamRuleService 建一条该节点专属的端口转发规则。
func (s *RemoteDesktopPolicyService) createIsolatedRule(ctx context.Context, node models.Node) (models.StreamRule, error) {
	port, err := s.allocateStreamPort(ctx)
	if err != nil {
		return models.StreamRule{}, err
	}
	enabled := true
	rule, err := s.streams.Create(ctx, node.TenantID, StreamRuleInput{
		Name:       fmt.Sprintf("远程桌面隔离隧道 %s", shortNodeLabel(node.ID)),
		Protocol:   "tcp",
		ListenPort: port,
		TargetType: "node",
		Target:     fmt.Sprintf("%s:%d", node.ID.String(), remoteDesktopIsolatedTargetPort),
		Enabled:    &enabled,
	})
	if err != nil {
		return models.StreamRule{}, err
	}
	return rule, nil
}

// allocateStreamPort 在已发布的端口转发区间里找一个未占用的 TCP 监听端口。
func (s *RemoteDesktopPolicyService) allocateStreamPort(ctx context.Context) (int, error) {
	minPort, maxPort := s.streams.PortRange()
	if minPort <= 0 || maxPort <= 0 || maxPort < minPort {
		return 0, fmt.Errorf("%w: 端口转发区间未配置，无法创建单独隧道", ErrConflict)
	}
	var used []int
	if err := s.db.WithContext(ctx).Model(&models.StreamRule{}).
		Where("protocol = ? AND listen_port BETWEEN ? AND ?", "tcp", minPort, maxPort).
		Pluck("listen_port", &used).Error; err != nil {
		return 0, fmt.Errorf("load used stream ports: %w", err)
	}
	occupied := make(map[int]struct{}, len(used))
	for _, port := range used {
		occupied[port] = struct{}{}
	}
	for port := minPort; port <= maxPort; port++ {
		if _, exists := occupied[port]; !exists {
			return port, nil
		}
	}
	return 0, fmt.Errorf("%w: 端口转发区间 %d-%d 已用尽", ErrConflict, minPort, maxPort)
}

// resolveJoinNetwork 决定加入哪个虚拟网络：显式指定优先，否则当租户只有一个网络时自动选中。
func (s *RemoteDesktopPolicyService) resolveJoinNetwork(ctx context.Context, node models.Node, requested *uuid.UUID) (uuid.UUID, error) {
	if requested != nil {
		network, err := s.networks.Get(ctx, *requested, &node.TenantID)
		if err != nil {
			return uuid.Nil, err
		}
		return network.ID, nil
	}
	list, err := s.networks.List(ctx, &node.TenantID)
	if err != nil {
		return uuid.Nil, err
	}
	switch len(list.Items) {
	case 0:
		return uuid.Nil, fmt.Errorf("%w: 该租户还没有虚拟网络，无法加入 Mesh", ErrInvalidInput)
	case 1:
		return list.Items[0].ID, nil
	default:
		return uuid.Nil, fmt.Errorf("%w: 该租户有多个虚拟网络，请指定 network_id", ErrInvalidInput)
	}
}

func (s *RemoteDesktopPolicyService) leaveAllNetworks(ctx context.Context, node models.Node, nodeID uuid.UUID) error {
	var members []models.NetworkMember
	if err := s.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&members).Error; err != nil {
		return fmt.Errorf("load network memberships: %w", err)
	}
	for _, member := range members {
		if err := s.networks.RemoveMember(ctx, member.NetworkID, nodeID, &node.TenantID); err != nil {
			return err
		}
	}
	return nil
}

func (s *RemoteDesktopPolicyService) getNode(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.Node, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var node models.Node
	err := query.First(&node).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Node{}, ErrNotFound
	}
	if err != nil {
		return models.Node{}, fmt.Errorf("get node for policy: %w", err)
	}
	return node, nil
}

func (s *RemoteDesktopPolicyService) isMember(ctx context.Context, nodeID uuid.UUID) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.NetworkMember{}).
		Where("node_id = ?", nodeID).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check network membership: %w", err)
	}
	return count > 0, nil
}

func (s *RemoteDesktopPolicyService) loadPolicy(ctx context.Context, nodeID uuid.UUID) (models.RemoteDesktopDevicePolicy, bool, error) {
	var policy models.RemoteDesktopDevicePolicy
	err := s.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.RemoteDesktopDevicePolicy{}, false, nil
	}
	if err != nil {
		return models.RemoteDesktopDevicePolicy{}, false, fmt.Errorf("load remote desktop policy: %w", err)
	}
	return policy, true, nil
}

func (s *RemoteDesktopPolicyService) loadPolicies(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]models.RemoteDesktopDevicePolicy, error) {
	result := make(map[uuid.UUID]models.RemoteDesktopDevicePolicy, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var items []models.RemoteDesktopDevicePolicy
	if err := s.db.WithContext(ctx).Where("node_id IN ?", ids).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("load remote desktop policies: %w", err)
	}
	for _, item := range items {
		result[item.NodeID] = item
	}
	return result, nil
}

// loadMeshStates 读现有 NetworkMember；一个节点在多个网络时取最近加入的那个。
func (s *RemoteDesktopPolicyService) loadMeshStates(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]nodeMeshState, error) {
	result := make(map[uuid.UUID]nodeMeshState, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var members []models.NetworkMember
	if err := s.db.WithContext(ctx).Where("node_id IN ?", ids).
		Order("joined_at DESC, id DESC").Find(&members).Error; err != nil {
		return nil, fmt.Errorf("load remote desktop mesh state: %w", err)
	}
	for _, member := range members {
		if _, exists := result[member.NodeID]; !exists {
			result[member.NodeID] = nodeMeshState{NetworkID: member.NetworkID, VirtualIP: member.VirtualIP}
		}
	}
	return result, nil
}

// loadSubnetRouteStatus 读现有 SubnetRoute 派生只读状态：有启用→ready；全禁用→degraded；无→unavailable。
func (s *RemoteDesktopPolicyService) loadSubnetRouteStatus(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	result := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var routes []models.SubnetRoute
	if err := s.db.WithContext(ctx).Where("node_id IN ?", ids).Find(&routes).Error; err != nil {
		return nil, fmt.Errorf("load remote desktop subnet routes: %w", err)
	}
	type routeCount struct{ total, enabled int }
	counts := make(map[uuid.UUID]*routeCount)
	for _, route := range routes {
		count := counts[route.NodeID]
		if count == nil {
			count = &routeCount{}
			counts[route.NodeID] = count
		}
		count.total++
		if route.Enabled {
			count.enabled++
		}
	}
	for id, count := range counts {
		switch {
		case count.total == 0:
			result[id] = SubnetRoutesUnavailable
		case count.enabled > 0:
			result[id] = SubnetRoutesReady
		default:
			result[id] = SubnetRoutesDegraded
		}
	}
	return result, nil
}

func (s *RemoteDesktopPolicyService) savePolicy(ctx context.Context, policy *models.RemoteDesktopDevicePolicy, exists bool) error {
	if exists {
		// 用 map 更新：显式写入 false/空值，避免 GORM 因零值而跳过字段。
		if err := s.db.WithContext(ctx).Model(&models.RemoteDesktopDevicePolicy{}).
			Where("node_id = ?", policy.NodeID).
			Updates(map[string]any{
				"remote_control_allowed":  policy.RemoteControlAllowed,
				"tunnel_mode":             policy.TunnelMode,
				"isolated_tunnel_enabled": policy.IsolatedTunnelEnabled,
				"isolated_stream_rule_id": policy.IsolatedStreamRuleID,
				"updated_at":              time.Now().UTC(),
			}).Error; err != nil {
			return fmt.Errorf("update remote desktop policy: %w", err)
		}
		return nil
	}
	if err := s.db.WithContext(ctx).Create(policy).Error; err != nil {
		return fmt.Errorf("create remote desktop policy: %w", err)
	}
	return nil
}

func buildPolicyView(
	nodeID uuid.UUID,
	policy models.RemoteDesktopDevicePolicy,
	mesh nodeMeshState,
	meshJoined bool,
	subnetRoutes string,
) RemoteDesktopDevicePolicyView {
	mode := strings.TrimSpace(policy.TunnelMode)
	if mode == "" {
		mode = TunnelModeAuto
	}
	if strings.TrimSpace(subnetRoutes) == "" {
		subnetRoutes = SubnetRoutesUnavailable
	}
	view := RemoteDesktopDevicePolicyView{
		NodeID:               nodeID,
		RemoteControlAllowed: policy.RemoteControlAllowed,
		TunnelMode:           mode,
		IsolatedTunnel: RemoteDesktopIsolatedTunnelView{
			Enabled:      policy.IsolatedTunnelEnabled,
			StreamRuleID: policy.IsolatedStreamRuleID,
		},
		Mesh:     RemoteDesktopMeshView{Joined: meshJoined},
		Readonly: RemoteDesktopReadonlyView{SubnetRoutes: subnetRoutes},
	}
	if meshJoined {
		networkID := mesh.NetworkID
		virtualIP := mesh.VirtualIP
		view.Mesh.NetworkID = &networkID
		view.Mesh.VirtualIP = &virtualIP
	}
	return view
}

func normalizeTunnelMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case TunnelModeAuto, TunnelModeDirect, TunnelModeRelay:
		return mode, nil
	default:
		return "", fmt.Errorf("%w: tunnel_mode must be auto, direct, or relay", ErrInvalidInput)
	}
}

func shortNodeLabel(id uuid.UUID) string {
	text := id.String()
	if len(text) > 8 {
		return text[:8]
	}
	return text
}
