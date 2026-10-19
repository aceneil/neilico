package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
	aclengine "neilico/control-plane/internal/service/acl"
	"neilico/control-plane/internal/service/cert"
	"neilico/control-plane/internal/validation"
)

type NetworkService struct {
	db     *gorm.DB
	crypto *cert.Crypto
}

func NewNetworkService(db *gorm.DB, crypto *cert.Crypto) *NetworkService {
	return &NetworkService{db: db, crypto: crypto}
}

type NetworkInput struct {
	Name string `json:"name"`
	CIDR string `json:"cidr"`
}

type NetworkCreateOutput struct {
	models.VirtualNetwork
	NetworkSecret string `json:"network_secret"`
	PresharedKey  string `json:"preshared_key"`
}

type NetworkList struct {
	Items []models.VirtualNetwork `json:"items"`
	Total int64                   `json:"total"`
}

func (s *NetworkService) List(ctx context.Context, tenantID *uuid.UUID) (NetworkList, error) {
	query := s.db.WithContext(ctx).Model(&models.VirtualNetwork{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return NetworkList{}, fmt.Errorf("count virtual networks: %w", err)
	}
	var items []models.VirtualNetwork
	if err := query.Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
		return NetworkList{}, fmt.Errorf("list virtual networks: %w", err)
	}
	return NetworkList{Items: items, Total: total}, nil
}

func (s *NetworkService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.VirtualNetwork, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.VirtualNetwork
	err := query.First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.VirtualNetwork{}, ErrNotFound
	}
	if err != nil {
		return models.VirtualNetwork{}, fmt.Errorf("get virtual network: %w", err)
	}
	return item, nil
}

func (s *NetworkService) Secret(ctx context.Context, network models.VirtualNetwork) (string, error) {
	if network.Secret == "" {
		return "", fmt.Errorf("network secret is unavailable")
	}
	return s.crypto.Decrypt(network.Secret)
}

func (s *NetworkService) Create(ctx context.Context, tenantID uuid.UUID, input NetworkInput) (NetworkCreateOutput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return NetworkCreateOutput{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	prefix, err := validation.NetworkCIDR(input.CIDR)
	if err != nil {
		return NetworkCreateOutput{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.checkUniqueAndOverlap(ctx, tenantID, uuid.Nil, name, prefix); err != nil {
		return NetworkCreateOutput{}, err
	}
	secret, err := generateNetworkSecret()
	if err != nil {
		return NetworkCreateOutput{}, err
	}
	psk, err := generateNetworkSecret()
	if err != nil {
		return NetworkCreateOutput{}, err
	}
	encryptedPSK, err := s.crypto.Encrypt(psk)
	if err != nil {
		return NetworkCreateOutput{}, fmt.Errorf("encrypt network preshared key: %w", err)
	}
	encrypted, err := s.crypto.Encrypt(secret)
	if err != nil {
		return NetworkCreateOutput{}, fmt.Errorf("encrypt network secret: %w", err)
	}
	item := models.VirtualNetwork{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         name,
		CIDR:         prefix.String(),
		Secret:       encrypted,
		PresharedKey: encryptedPSK,
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return NetworkCreateOutput{}, ErrConflict
		}
		return NetworkCreateOutput{}, fmt.Errorf("create virtual network: %w", err)
	}
	return NetworkCreateOutput{VirtualNetwork: item, NetworkSecret: secret, PresharedKey: psk}, nil
}

func (s *NetworkService) Update(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID, input NetworkInput) (models.VirtualNetwork, error) {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return models.VirtualNetwork{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return models.VirtualNetwork{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	prefix, err := validation.NetworkCIDR(input.CIDR)
	if err != nil {
		return models.VirtualNetwork{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := s.checkUniqueAndOverlap(ctx, item.TenantID, item.ID, name, prefix); err != nil {
		return models.VirtualNetwork{}, err
	}
	if prefix.String() != item.CIDR {
		var members []models.NetworkMember
		if err := s.db.WithContext(ctx).Where("network_id = ?", item.ID).Find(&members).Error; err != nil {
			return models.VirtualNetwork{}, fmt.Errorf("load network members: %w", err)
		}
		for _, member := range members {
			if _, err := validation.NetworkVirtualIP(prefix, member.VirtualIP); err != nil {
				return models.VirtualNetwork{}, fmt.Errorf("%w: existing member %s is outside the new CIDR", ErrInvalidInput, member.NodeID)
			}
		}
	}
	item.Name = name
	item.CIDR = prefix.String()
	if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return models.VirtualNetwork{}, ErrConflict
		}
		return models.VirtualNetwork{}, fmt.Errorf("update virtual network: %w", err)
	}
	return item, nil
}

func (s *NetworkService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("network_id = ?", item.ID).Delete(&models.NetworkMember{}).Error; err != nil {
			return fmt.Errorf("delete network members: %w", err)
		}
		if err := tx.Where("network_id = ?", item.ID).Delete(&models.ACLRule{}).Error; err != nil {
			return fmt.Errorf("delete network ACL rules: %w", err)
		}
		if err := tx.Where("network_id = ?", item.ID).Delete(&models.SubnetRoute{}).Error; err != nil {
			return fmt.Errorf("delete network subnet routes: %w", err)
		}
		if err := tx.Delete(&models.VirtualNetwork{}, "id = ?", item.ID).Error; err != nil {
			return fmt.Errorf("delete virtual network: %w", err)
		}
		return nil
	})
}

func (s *NetworkService) checkUniqueAndOverlap(ctx context.Context, tenantID, exclude uuid.UUID, name string, prefix netip.Prefix) error {
	var networks []models.VirtualNetwork
	if err := s.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Find(&networks).Error; err != nil {
		return fmt.Errorf("load virtual networks: %w", err)
	}
	for _, candidate := range networks {
		if candidate.ID == exclude {
			continue
		}
		if strings.EqualFold(candidate.Name, name) {
			return ErrConflict
		}
		candidatePrefix, err := netip.ParsePrefix(candidate.CIDR)
		if err != nil {
			return fmt.Errorf("parse stored network CIDR: %w", err)
		}
		if candidatePrefix.Overlaps(prefix) {
			return ErrConflict
		}
	}
	return nil
}

type NetworkMemberInput struct {
	NodeID    uuid.UUID `json:"node_id"`
	VirtualIP string    `json:"virtual_ip,omitempty"`
	Role      string    `json:"role,omitempty"`
}

type NetworkMemberList struct {
	Items []models.NetworkMember `json:"items"`
	Total int64                  `json:"total"`
}

func (s *NetworkService) ListMembers(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID) (NetworkMemberList, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return NetworkMemberList{}, err
	}
	var items []models.NetworkMember
	if err := s.db.WithContext(ctx).Where("network_id = ?", network.ID).Order("joined_at ASC, id ASC").Find(&items).Error; err != nil {
		return NetworkMemberList{}, fmt.Errorf("list network members: %w", err)
	}
	return NetworkMemberList{Items: items, Total: int64(len(items))}, nil
}

func (s *NetworkService) AddMember(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID, input NetworkMemberInput) (models.NetworkMember, error) {
	return s.addMember(ctx, networkID, tenantID, input, true)
}

// addMemberForPolicyUpdate 仅供策略服务在写入 forced 状态时复用成员能力。
// 此时新策略尚未落库，若沿用公网入口的 deny 检查会把服务端自己的强制启用误拒绝。
func (s *NetworkService) addMemberForPolicyUpdate(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID, input NetworkMemberInput) (models.NetworkMember, error) {
	return s.addMember(ctx, networkID, tenantID, input, false)
}

func (s *NetworkService) addMember(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID, input NetworkMemberInput, enforceFeaturePolicy bool) (models.NetworkMember, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return models.NetworkMember{}, err
	}
	prefix, err := netip.ParsePrefix(network.CIDR)
	if err != nil {
		return models.NetworkMember{}, fmt.Errorf("parse network CIDR: %w", err)
	}
	var node models.Node
	err = s.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", input.NodeID, network.TenantID).First(&node).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.NetworkMember{}, ErrNotFound
	}
	if err != nil {
		return models.NetworkMember{}, fmt.Errorf("get member node: %w", err)
	}
	if enforceFeaturePolicy {
		meshState, err := deviceFeatureState(ctx, s.db, node.ID, FeatureMesh)
		if err != nil {
			return models.NetworkMember{}, err
		}
		if !meshState.Allowed() {
			return models.NetworkMember{}, fmt.Errorf("%w: mesh is denied by server device policy", ErrForbidden)
		}
	}
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = "member"
	}
	if role != "member" && role != "gateway" && role != "exit" {
		return models.NetworkMember{}, fmt.Errorf("%w: role must be member, gateway, or exit", ErrInvalidInput)
	}
	input.Role = role
	for attempt := 0; attempt < 5; attempt++ {
		member, retry, err := s.tryAddMember(ctx, network, prefix, input)
		if err != nil {
			return models.NetworkMember{}, err
		}
		if !retry {
			return member, nil
		}
	}
	return models.NetworkMember{}, ErrConflict
}

func (s *NetworkService) tryAddMember(ctx context.Context, network models.VirtualNetwork, prefix netip.Prefix, input NetworkMemberInput) (models.NetworkMember, bool, error) {
	var member models.NetworkMember
	retry := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var virtualIP netip.Addr
		if strings.TrimSpace(input.VirtualIP) != "" {
			parsed, err := validation.NetworkVirtualIP(prefix, input.VirtualIP)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidInput, err)
			}
			virtualIP = parsed
		} else {
			allocated, err := allocateVirtualIP(ctx, tx, network.ID, prefix)
			if err != nil {
				return err
			}
			virtualIP = allocated
		}
		var conflictCount int64
		if err := tx.Model(&models.NetworkMember{}).
			Where("network_id = ? AND (node_id = ? OR virtual_ip = ?)", network.ID, input.NodeID, virtualIP.String()).
			Count(&conflictCount).Error; err != nil {
			return fmt.Errorf("check network member conflicts: %w", err)
		}
		if conflictCount > 0 {
			return ErrConflict
		}
		member = models.NetworkMember{
			ID:        uuid.New(),
			NetworkID: network.ID,
			NodeID:    input.NodeID,
			VirtualIP: virtualIP.String(),
			Role:      input.Role,
			JoinedAt:  time.Now().UTC(),
		}
		if err := tx.Create(&member).Error; err != nil {
			if isUniqueViolation(err) {
				retry = true
				return errUniqueRace
			}
			return fmt.Errorf("create network member: %w", err)
		}
		return nil
	})
	if errors.Is(err, errUniqueRace) {
		return models.NetworkMember{}, retry, nil
	}
	if err != nil {
		return models.NetworkMember{}, false, err
	}
	return member, false, nil
}

var errUniqueRace = errors.New("network member unique race")

func allocateVirtualIP(ctx context.Context, tx *gorm.DB, networkID uuid.UUID, prefix netip.Prefix) (netip.Addr, error) {
	var occupiedValues []string
	if err := tx.WithContext(ctx).Model(&models.NetworkMember{}).Where("network_id = ?", networkID).Pluck("virtual_ip", &occupiedValues).Error; err != nil {
		return netip.Addr{}, fmt.Errorf("load allocated virtual IPs: %w", err)
	}
	occupied := make(map[netip.Addr]struct{}, len(occupiedValues))
	for _, value := range occupiedValues {
		if addr, err := netip.ParseAddr(value); err == nil {
			occupied[addr.Unmap()] = struct{}{}
		}
	}
	network := prefix.Masked().Addr()
	broadcast := validation.LastIPv4(prefix)
	for candidate := network.Next(); candidate != network && candidate != broadcast; candidate = candidate.Next() {
		if validation.IsReservedNetworkIP(prefix, candidate) {
			continue
		}
		if _, exists := occupied[candidate]; !exists {
			return candidate, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("%w: network has no allocatable virtual IP", ErrConflict)
}

func (s *NetworkService) RemoveMember(ctx context.Context, networkID, nodeID uuid.UUID, tenantID *uuid.UUID) error {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Where("network_id = ? AND node_id = ?", network.ID, nodeID).Delete(&models.NetworkMember{})
	if result.Error != nil {
		return fmt.Errorf("delete network member: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

type ACLInput struct {
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Ports    string `json:"ports"`
	Priority int    `json:"priority"`
}

type ACLList struct {
	Items []models.ACLRule `json:"items"`
	Total int64            `json:"total"`
}

func (s *NetworkService) ListACL(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID) (ACLList, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return ACLList{}, err
	}
	var items []models.ACLRule
	if err := s.db.WithContext(ctx).Where("network_id = ?", network.ID).Order("priority ASC, id ASC").Find(&items).Error; err != nil {
		return ACLList{}, fmt.Errorf("list ACL rules: %w", err)
	}
	return ACLList{Items: items, Total: int64(len(items))}, nil
}

func (s *NetworkService) CreateACL(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID, input ACLInput) (models.ACLRule, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return models.ACLRule{}, err
	}
	item := models.ACLRule{
		ID:        uuid.New(),
		NetworkID: network.ID,
		Src:       strings.TrimSpace(input.Src),
		Dst:       strings.TrimSpace(input.Dst),
		Action:    strings.ToLower(strings.TrimSpace(input.Action)),
		Protocol:  strings.ToLower(strings.TrimSpace(input.Protocol)),
		Ports:     strings.TrimSpace(input.Ports),
		Priority:  input.Priority,
	}
	if item.Protocol == "" {
		item.Protocol = "any"
	}
	if item.Ports == "" {
		item.Ports = "any"
	}
	if err := s.validateACLRule(ctx, network.ID, item); err != nil {
		return models.ACLRule{}, err
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return models.ACLRule{}, fmt.Errorf("create ACL rule: %w", err)
	}
	return item, nil
}

func (s *NetworkService) DeleteACL(ctx context.Context, networkID, ruleID uuid.UUID, tenantID *uuid.UUID) error {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Where("id = ? AND network_id = ?", ruleID, network.ID).Delete(&models.ACLRule{})
	if result.Error != nil {
		return fmt.Errorf("delete ACL rule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *NetworkService) validateACLRule(ctx context.Context, networkID uuid.UUID, item models.ACLRule) error {
	rule := aclengine.Rule{Src: item.Src, Dst: item.Dst, Action: item.Action, Protocol: item.Protocol, Ports: item.Ports}
	if err := aclengine.Validate(rule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	for _, expression := range []string{item.Src, item.Dst} {
		reference := aclReferenceID(expression)
		if reference == uuid.Nil {
			continue
		}
		var count int64
		if err := s.db.WithContext(ctx).Model(&models.NetworkMember{}).
			Where("network_id = ? AND node_id = ?", networkID, reference).Count(&count).Error; err != nil {
			return fmt.Errorf("check ACL member reference: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("%w: ACL member reference %s is not in the network", ErrInvalidInput, reference)
		}
	}
	return nil
}

func aclReferenceID(expression string) uuid.UUID {
	value := strings.ToLower(strings.TrimSpace(expression))
	for _, prefix := range []string{"member:", "node:", "member/", "node/"} {
		value = strings.TrimPrefix(value, prefix)
	}
	id, _ := uuid.Parse(value)
	return id
}

type RouteInput struct {
	NodeID uuid.UUID `json:"node_id"`
	CIDR   string    `json:"cidr"`
	// Site 是操作者声明的「同一个实体局域网」标识；留空按节点 ID 单独成站（不与他人合并）。
	Site     *string `json:"site,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
	Priority *int    `json:"priority,omitempty"`
}

// RouteView 在路由本体上附加「此刻是否生效」，供 UI 标出主用/备用。
type RouteView struct {
	models.SubnetRoute
	Active bool `json:"active"`
	// EffectiveSite 是自动/手动解析后的站点归属，UI 用它展示主备分组。
	EffectiveSite string `json:"effective_site"`
}

type RouteList struct {
	Items []RouteView `json:"items"`
	Total int64       `json:"total"`
}

func (s *NetworkService) ListRoutes(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID) (RouteList, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return RouteList{}, err
	}
	var items []models.SubnetRoute
	if err := s.db.WithContext(ctx).Where("network_id = ?", network.ID).Order("cidr ASC, priority ASC, id ASC").Find(&items).Error; err != nil {
		return RouteList{}, fmt.Errorf("list subnet routes: %w", err)
	}
	nodeIDs := make([]uuid.UUID, 0, len(items))
	seen := map[uuid.UUID]bool{}
	for _, item := range items {
		if !seen[item.NodeID] {
			seen[item.NodeID] = true
			nodeIDs = append(nodeIDs, item.NodeID)
		}
	}
	available := map[uuid.UUID]bool{}
	nodeByID := map[uuid.UUID]models.Node{}
	if len(nodeIDs) > 0 {
		var nodes []models.Node
		if err := s.db.WithContext(ctx).Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
			return RouteList{}, fmt.Errorf("load subnet route nodes: %w", err)
		}
		for _, node := range nodes {
			available[node.ID] = NodeAvailableForRouting(node)
			nodeByID[node.ID] = node
		}
	}
	active := SelectActiveRoutes(items, func(nodeID uuid.UUID) bool { return available[nodeID] })
	views := make([]RouteView, 0, len(items))
	for _, item := range items {
		winner, ok := active[item.CIDR]
		views = append(views, RouteView{
			SubnetRoute:   item,
			Active:        ok && winner.ID == item.ID,
			EffectiveSite: ResolveSite(item.Site, nodeByID[item.NodeID]),
		})
	}
	return RouteList{Items: views, Total: int64(len(views))}, nil
}

func (s *NetworkService) CreateRoute(ctx context.Context, networkID uuid.UUID, tenantID *uuid.UUID, input RouteInput) (models.SubnetRoute, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return models.SubnetRoute{}, err
	}
	item, err := s.newRoute(ctx, network.ID, uuid.Nil, input)
	if err != nil {
		return models.SubnetRoute{}, err
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return models.SubnetRoute{}, ErrConflict
		}
		return models.SubnetRoute{}, fmt.Errorf("create subnet route: %w", err)
	}
	return item, nil
}

func (s *NetworkService) UpdateRoute(ctx context.Context, networkID, routeID uuid.UUID, tenantID *uuid.UUID, input RouteInput) (models.SubnetRoute, error) {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return models.SubnetRoute{}, err
	}
	var existing models.SubnetRoute
	err = s.db.WithContext(ctx).Where("id = ? AND network_id = ?", routeID, network.ID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.SubnetRoute{}, ErrNotFound
	}
	if err != nil {
		return models.SubnetRoute{}, fmt.Errorf("get subnet route: %w", err)
	}
	item, err := s.newRoute(ctx, network.ID, existing.ID, input)
	if err != nil {
		return models.SubnetRoute{}, err
	}
	item.ID = existing.ID
	if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return models.SubnetRoute{}, ErrConflict
		}
		return models.SubnetRoute{}, fmt.Errorf("update subnet route: %w", err)
	}
	return item, nil
}

// newRoute 归一化并校验一条子网路由。
//
// 冗余语义：**同一条 CIDR 允许挂在多台节点上**（同一实体局域网的多台设备互为备份），
// 但同一节点重复宣告同一 CIDR 没有意义；不同 CIDR 之间也不允许重叠（/24 与 /16 会让
// 最长前缀匹配把流量劈成两半）。
func (s *NetworkService) newRoute(ctx context.Context, networkID uuid.UUID, excludeID uuid.UUID, input RouteInput) (models.SubnetRoute, error) {
	prefix, err := validation.RouteCIDR(input.CIDR)
	if err != nil {
		return models.SubnetRoute{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	priority := DefaultRoutePriority
	if input.Priority != nil {
		priority = *input.Priority
	}
	site := ""
	if input.Site != nil {
		site = strings.TrimSpace(*input.Site)
	}
	if len([]rune(site)) > 64 {
		return models.SubnetRoute{}, fmt.Errorf("%w: site must be at most 64 characters", ErrInvalidInput)
	}
	if priority < MinRoutePriority || priority > MaxRoutePriority {
		return models.SubnetRoute{}, fmt.Errorf("%w: priority must be between %d and %d", ErrInvalidInput, MinRoutePriority, MaxRoutePriority)
	}
	var candidateNode models.Node
	if err := s.db.WithContext(ctx).First(&candidateNode, "id = ?", input.NodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.SubnetRoute{}, ErrNotFound
		}
		return models.SubnetRoute{}, fmt.Errorf("load subnet route node: %w", err)
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.NetworkMember{}).
		Where("network_id = ? AND node_id = ?", networkID, input.NodeID).Count(&count).Error; err != nil {
		return models.SubnetRoute{}, fmt.Errorf("check subnet route node: %w", err)
	}
	if count == 0 {
		return models.SubnetRoute{}, ErrNotFound
	}
	candidateSite := ResolveSite(site, candidateNode)
	var existing []models.SubnetRoute
	if err := s.db.WithContext(ctx).Where("network_id = ?", networkID).Find(&existing).Error; err != nil {
		return models.SubnetRoute{}, fmt.Errorf("load existing subnet routes: %w", err)
	}
	for _, item := range existing {
		if item.ID == excludeID {
			continue
		}
		if item.CIDR == prefix.String() {
			var existingNode models.Node
			if err := s.db.WithContext(ctx).First(&existingNode, "id = ?", item.NodeID).Error; err != nil {
				continue
			}
			existingSite := ResolveSite(item.Site, existingNode)
			if existingSite != candidateSite {
				// 不同站点用同一网段：主备会互相串，直接拒绝并说明。
				return models.SubnetRoute{}, &RouteConflictError{Reason: fmt.Sprintf(
					"%s 已被站点「%s」使用；同一虚拟网络里同一网段只能属于一个站点（要么改网段，要么放进不同网络）", prefix, existingSite)}
			}
			if item.NodeID == input.NodeID {
				return models.SubnetRoute{}, &RouteConflictError{Reason: fmt.Sprintf("该节点已经宣告了 %s", prefix)}
			}
			continue
		}
		other, parseErr := netip.ParsePrefix(item.CIDR)
		if parseErr != nil {
			continue
		}
		if other.Overlaps(prefix) {
			return models.SubnetRoute{}, &RouteConflictError{Reason: fmt.Sprintf("%s 与已有路由 %s 重叠，请改用不重叠的网段", prefix, item.CIDR)}
		}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return models.SubnetRoute{
		ID:        uuid.New(),
		NetworkID: networkID,
		NodeID:    input.NodeID,
		CIDR:      prefix.String(),
		Site:      site,
		Enabled:   enabled,
		Priority:  priority,
	}, nil
}

func (s *NetworkService) DeleteRoute(ctx context.Context, networkID, routeID uuid.UUID, tenantID *uuid.UUID) error {
	network, err := s.Get(ctx, networkID, tenantID)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Where("id = ? AND network_id = ?", routeID, network.ID).Delete(&models.SubnetRoute{})
	if result.Error != nil {
		return fmt.Errorf("delete subnet route: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *NetworkService) RotatePSK(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (string, error) {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return "", err
	}
	psk, err := generateNetworkSecret()
	if err != nil {
		return "", err
	}
	encrypted, err := s.crypto.Encrypt(psk)
	if err != nil {
		return "", fmt.Errorf("encrypt rotated network preshared key: %w", err)
	}
	if err := s.db.WithContext(ctx).Model(&models.VirtualNetwork{}).
		Where("id = ?", item.ID).Update("preshared_key", encrypted).Error; err != nil {
		return "", fmt.Errorf("store rotated network preshared key: %w", err)
	}
	return psk, nil
}

func generateNetworkSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate network secret: %w", err)
	}
	// ⚠️ 必须用**标准 base64**：这个值会被当作 WireGuard 的 PresharedKey 下发，
	// 而 wg 的解析器只接受标准 base64。历史上这里用的是 base64.RawURLEncoding，
	// 于是密钥里会出现 '-'/'_' 且缺少 '=' 填充，agent 执行 `wg setconf` 时报
	//   failed to parse base64-encoded key: illegal base64 data at input byte 20
	// 导致**该网络下所有节点**的配置应用失败、mesh 根本起不来（实测）。
	return base64.StdEncoding.EncodeToString(raw), nil
}
