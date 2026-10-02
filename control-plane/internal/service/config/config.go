package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"umpp/control-plane/internal/metrics"
	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service"
	aclengine "umpp/control-plane/internal/service/acl"
	"umpp/control-plane/internal/service/cert"
	"umpp/control-plane/internal/service/mesh"
	"umpp/control-plane/internal/service/mesh/wireguard"
)

const (
	TargetNode    = "node"
	TargetNetwork = "network"
	TargetProxy   = "proxy"
)

type Manager struct {
	db       *gorm.DB
	crypto   *cert.Crypto
	provider mesh.Provider
	metrics  *metrics.Metrics
}

func New(db *gorm.DB, crypto *cert.Crypto, observer *metrics.Metrics) *Manager {
	return &Manager{db: db, crypto: crypto, provider: wireguard.New(), metrics: observer}
}

type NodeIdentity struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	VirtualIP      string    `json:"virtual_ip"`
	PublicEndpoint string    `json:"public_endpoint"`
}

type Peer struct {
	NodeID     string   `json:"node_id"`
	PublicKey  string   `json:"public_key"`
	Endpoint   string   `json:"endpoint"`
	AllowedIPs []string `json:"allowed_ips"`
	VirtualIP  string   `json:"virtual_ip"`
}

type Network struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CIDR          string `json:"cidr"`
	NetworkSecret string `json:"network_secret,omitempty"`
	PresharedKey  string `json:"preshared_key,omitempty"`
	Peers         []Peer `json:"peers"`
}

type ProxyRule struct {
	ID            uuid.UUID            `json:"id"`
	DomainID      uuid.UUID            `json:"domain_id"`
	Path          string               `json:"path"`
	TargetType    string               `json:"target_type"`
	Target        string               `json:"target"`
	AccessControl models.AccessControl `json:"access_control"`
	Enabled       bool                 `json:"enabled"`
}

type ACLRule struct {
	ID       uuid.UUID `json:"id"`
	Src      string    `json:"src"`
	Dst      string    `json:"dst"`
	Action   string    `json:"action"`
	Protocol string    `json:"protocol"`
	Ports    string    `json:"ports"`
	Priority int       `json:"priority"`
}

type Route struct {
	ID      uuid.UUID `json:"id"`
	NodeID  uuid.UUID `json:"node_id"`
	CIDR    string    `json:"cidr"`
	Enabled bool      `json:"enabled"`
}

type NodeConfig struct {
	Node           NodeIdentity `json:"node"`
	Network        *Network     `json:"network"`
	ProxyRules     []ProxyRule  `json:"proxy_rules"`
	ACL            []ACLRule    `json:"acl"`
	Routes         []Route      `json:"routes"`
	PolicyFiltered bool         `json:"policy_filtered"`
}

type Delivery struct {
	Version         int          `json:"version"`
	Node            NodeIdentity `json:"node"`
	Network         *Network     `json:"network"`
	ProxyRules      []ProxyRule  `json:"proxy_rules"`
	ACL             []ACLRule    `json:"acl"`
	Routes          []Route      `json:"routes"`
	PolicyFiltered  bool         `json:"policy_filtered"`
	WireGuardConfig string       `json:"wireguard_config,omitempty"`
}

type NetworkSnapshot struct {
	Network Network                `json:"network"`
	Members []models.NetworkMember `json:"members"`
	ACL     []ACLRule              `json:"acl"`
	Routes  []Route                `json:"routes"`
}

type Version struct {
	ID         uuid.UUID      `json:"id"`
	TargetType string         `json:"target_type"`
	TargetID   uuid.UUID      `json:"target_id"`
	Version    int            `json:"version"`
	Reason     string         `json:"reason,omitempty"`
	Summary    datatypes.JSON `json:"summary,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type VersionList struct {
	Items []Version `json:"items"`
	Total int64     `json:"total"`
}

func (m *Manager) BumpForNode(ctx context.Context, nodeID uuid.UUID, reason string) (models.ConfigVersion, error) {
	return m.bump(ctx, TargetNode, nodeID, reason, m.buildNodeSnapshot)
}

func (m *Manager) BumpForNetwork(ctx context.Context, networkID uuid.UUID, reason string) (models.ConfigVersion, error) {
	networkVersion, err := m.bump(ctx, TargetNetwork, networkID, reason, m.buildNetworkSnapshot)
	if err != nil {
		return models.ConfigVersion{}, err
	}
	var nodeIDs []uuid.UUID
	if err := m.db.WithContext(ctx).Model(&models.NetworkMember{}).
		Where("network_id = ?", networkID).Order("node_id").Pluck("node_id", &nodeIDs).Error; err != nil {
		return models.ConfigVersion{}, fmt.Errorf("load network member nodes: %w", err)
	}
	for _, nodeID := range nodeIDs {
		if _, err := m.BumpForNode(ctx, nodeID, reason); err != nil {
			return models.ConfigVersion{}, err
		}
	}
	return networkVersion, nil
}

func (m *Manager) BumpForProxy(ctx context.Context, proxyRuleID uuid.UUID, reason string) (models.ConfigVersion, error) {
	return m.bump(ctx, TargetProxy, proxyRuleID, reason, m.buildProxySnapshot)
}

func (m *Manager) buildNodeSnapshot(ctx context.Context, tx *gorm.DB, targetID uuid.UUID) (any, error) {
	var node models.Node
	if err := tx.WithContext(ctx).Where("id = ?", targetID).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, fmt.Errorf("load config node: %w", err)
	}
	return m.buildNodeConfig(ctx, tx, node)
}

func (m *Manager) buildNetworkSnapshot(ctx context.Context, tx *gorm.DB, targetID uuid.UUID) (any, error) {
	var network models.VirtualNetwork
	if err := tx.WithContext(ctx).Where("id = ?", targetID).First(&network).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, fmt.Errorf("load config network: %w", err)
	}
	return m.buildNetworkConfig(ctx, tx, network)
}

func (m *Manager) buildProxySnapshot(ctx context.Context, tx *gorm.DB, targetID uuid.UUID) (any, error) {
	var rule models.ProxyRule
	if err := tx.WithContext(ctx).Where("id = ?", targetID).First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, fmt.Errorf("load config proxy rule: %w", err)
	}
	return ProxyRule{
		ID: rule.ID, DomainID: rule.DomainID, Path: rule.Path, TargetType: rule.TargetType,
		Target: rule.Target, AccessControl: rule.AccessControl, Enabled: rule.Enabled,
	}, nil
}

func (m *Manager) bump(ctx context.Context, targetType string, targetID uuid.UUID, reason string, build func(context.Context, *gorm.DB, uuid.UUID) (any, error)) (models.ConfigVersion, error) {
	reason = truncate(reason, 255)
	if reason == "" {
		reason = "configuration change"
	}
	for attempt := 0; attempt < 5; attempt++ {
		version, retry, err := m.tryBump(ctx, targetType, targetID, reason, build)
		if err != nil {
			return models.ConfigVersion{}, err
		}
		if !retry {
			m.setConfigVersionMetric(targetType, targetID, version.Version)
			return version, nil
		}
	}
	return models.ConfigVersion{}, service.ErrConflict
}

func (m *Manager) tryBump(ctx context.Context, targetType string, targetID uuid.UUID, reason string, build func(context.Context, *gorm.DB, uuid.UUID) (any, error)) (models.ConfigVersion, bool, error) {
	var created models.ConfigVersion
	retry := false
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot, err := build(ctx, tx, targetID)
		if err != nil {
			return err
		}
		configJSON, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("marshal config snapshot: %w", err)
		}
		summaryJSON, err := summarizeConfig(snapshot)
		if err != nil {
			return err
		}
		var tenantID uuid.UUID
		switch targetType {
		case TargetNode:
			var node models.Node
			if err := tx.Where("id = ?", targetID).First(&node).Error; err != nil {
				return service.ErrNotFound
			}
			tenantID = node.TenantID
		case TargetNetwork:
			var network models.VirtualNetwork
			if err := tx.Where("id = ?", targetID).First(&network).Error; err != nil {
				return service.ErrNotFound
			}
			tenantID = network.TenantID
		case TargetProxy:
			var rule models.ProxyRule
			if err := tx.Where("id = ?", targetID).First(&rule).Error; err != nil {
				return service.ErrNotFound
			}
			tenantID = rule.TenantID
		default:
			return fmt.Errorf("%w: unsupported config target type", service.ErrInvalidInput)
		}
		var latest int
		if err := tx.Model(&models.ConfigVersion{}).
			Where("target_type = ? AND target_id = ?", targetType, targetID).
			Select("COALESCE(MAX(version), 0)").Scan(&latest).Error; err != nil {
			return fmt.Errorf("load latest config version: %w", err)
		}
		created = models.ConfigVersion{
			ID:         uuid.New(),
			TenantID:   tenantID,
			TargetType: targetType,
			TargetID:   targetID,
			Version:    latest + 1,
			Config:     configJSON,
			Reason:     reason,
			Summary:    summaryJSON,
		}
		if err := tx.Create(&created).Error; err != nil {
			if configUniqueViolation(err) {
				retry = true
				return errVersionRace
			}
			return fmt.Errorf("create config version: %w", err)
		}
		return nil
	})
	if errors.Is(err, errVersionRace) {
		return models.ConfigVersion{}, retry, nil
	}
	if err != nil {
		return models.ConfigVersion{}, false, err
	}
	return created, false, nil
}

var errVersionRace = errors.New("config version unique race")

func (m *Manager) buildNodeConfig(ctx context.Context, db *gorm.DB, node models.Node) (NodeConfig, error) {
	var members []models.NetworkMember
	if err := db.WithContext(ctx).Where("node_id = ?", node.ID).Order("joined_at ASC, id ASC").Find(&members).Error; err != nil {
		return NodeConfig{}, fmt.Errorf("load node network memberships: %w", err)
	}
	config := NodeConfig{
		Node: NodeIdentity{
			ID:             node.ID,
			Name:           node.Name,
			PublicEndpoint: stringValue(node.PublicEndpoint),
		},
		ProxyRules: []ProxyRule{},
		ACL:        []ACLRule{},
		Routes:     []Route{},
	}
	if len(members) == 0 {
		config.Network = nil
	} else {
		primary := members[0]
		config.Node.VirtualIP = primary.VirtualIP
		var network models.VirtualNetwork
		if err := db.WithContext(ctx).Where("id = ?", primary.NetworkID).First(&network).Error; err != nil {
			return NodeConfig{}, fmt.Errorf("load node primary network: %w", err)
		}
		built, err := m.buildNetworkConfig(ctx, db, network)
		if err != nil {
			return NodeConfig{}, err
		}
		networkConfig := built.Network
		networkConfig.NetworkSecret = ""
		networkConfig.PresharedKey = ""
		networkConfig.Peers, config.PolicyFiltered = filterPeers(primary, networkConfig.Peers, built.ACL)
		if m.metrics != nil && config.PolicyFiltered {
			m.metrics.IncACLDenied()
		}
		config.Network = &networkConfig
		config.ACL = built.ACL
		config.Routes = built.Routes
	}
	var proxyRules []models.ProxyRule
	if err := db.WithContext(ctx).Where("tenant_id = ?", node.TenantID).
		Order("created_at ASC, id ASC").Find(&proxyRules).Error; err != nil {
		return NodeConfig{}, fmt.Errorf("load node proxy rules: %w", err)
	}
	for _, rule := range proxyRules {
		config.ProxyRules = append(config.ProxyRules, ProxyRule{
			ID:            rule.ID,
			DomainID:      rule.DomainID,
			Path:          rule.Path,
			TargetType:    rule.TargetType,
			Target:        rule.Target,
			AccessControl: rule.AccessControl,
			Enabled:       rule.Enabled,
		})
	}
	return config, nil
}

func (m *Manager) buildNetworkConfig(ctx context.Context, db *gorm.DB, network models.VirtualNetwork) (NetworkSnapshot, error) {
	var members []models.NetworkMember
	if err := db.WithContext(ctx).Where("network_id = ?", network.ID).Order("joined_at ASC, id ASC").Find(&members).Error; err != nil {
		return NetworkSnapshot{}, fmt.Errorf("load network members: %w", err)
	}
	var aclRules []models.ACLRule
	if err := db.WithContext(ctx).Where("network_id = ?", network.ID).Order("priority ASC, id ASC").Find(&aclRules).Error; err != nil {
		return NetworkSnapshot{}, fmt.Errorf("load network ACL rules: %w", err)
	}
	var routes []models.SubnetRoute
	if err := db.WithContext(ctx).Where("network_id = ?", network.ID).Order("cidr ASC, node_id ASC, id ASC").Find(&routes).Error; err != nil {
		return NetworkSnapshot{}, fmt.Errorf("load network routes: %w", err)
	}
	nodes := make(map[uuid.UUID]models.Node, len(members))
	var nodeIDs []uuid.UUID
	for _, member := range members {
		nodeIDs = append(nodeIDs, member.NodeID)
	}
	if len(nodeIDs) > 0 {
		var found []models.Node
		if err := db.WithContext(ctx).Where("id IN ?", nodeIDs).Find(&found).Error; err != nil {
			return NetworkSnapshot{}, fmt.Errorf("load network nodes: %w", err)
		}
		for _, node := range found {
			nodes[node.ID] = node
		}
	}
	peers := make([]Peer, 0, len(members))
	for _, member := range members {
		node := nodes[member.NodeID]
		allowed := []string{member.VirtualIP + "/32"}
		for _, route := range routes {
			if route.Enabled && route.NodeID == member.NodeID {
				allowed = append(allowed, route.CIDR)
			}
		}
		sort.Strings(allowed)
		peers = append(peers, Peer{
			NodeID:     member.NodeID.String(),
			PublicKey:  node.PublicKey,
			Endpoint:   stringValue(node.PublicEndpoint),
			AllowedIPs: allowed,
			VirtualIP:  member.VirtualIP,
		})
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].PublicKey == peers[j].PublicKey {
			return peers[i].NodeID < peers[j].NodeID
		}
		return peers[i].PublicKey < peers[j].PublicKey
	})
	convertedACL := make([]ACLRule, 0, len(aclRules))
	for _, rule := range aclRules {
		convertedACL = append(convertedACL, ACLRule{
			ID: rule.ID, Src: rule.Src, Dst: rule.Dst, Action: rule.Action,
			Protocol: rule.Protocol, Ports: rule.Ports, Priority: rule.Priority,
		})
	}
	convertedRoutes := make([]Route, 0, len(routes))
	for _, route := range routes {
		convertedRoutes = append(convertedRoutes, Route{
			ID: route.ID, NodeID: route.NodeID, CIDR: route.CIDR, Enabled: route.Enabled,
		})
	}
	return NetworkSnapshot{
		Network: Network{
			ID: network.ID.String(), Name: network.Name, CIDR: network.CIDR, Peers: peers,
		},
		Members: members,
		ACL:     convertedACL,
		Routes:  convertedRoutes,
	}, nil
}

func filterPeers(localMember models.NetworkMember, peers []Peer, rules []ACLRule) ([]Peer, bool) {
	filtered := false
	result := make([]Peer, 0, len(peers))
	references := memberReferences(peers)
	for _, peer := range peers {
		if peer.NodeID == localMember.NodeID.String() {
			continue
		}
		decision := aclengine.Allow
		if len(rules) > 0 {
			converted := make([]aclengine.Rule, 0, len(rules))
			for _, rule := range rules {
				converted = append(converted, aclengine.Rule{
					Src: rule.Src, Dst: rule.Dst, Action: rule.Action,
					Protocol: rule.Protocol, Ports: rule.Ports, Priority: rule.Priority,
					References: references,
				})
			}
			decision = aclengine.Match(converted, aclengine.Packet{
				SrcIP: parseAddr(localMember.VirtualIP), DstIP: parseAddr(peer.VirtualIP),
				Protocol: "any", DstPort: 0,
			})
		}
		if decision == aclengine.Deny {
			filtered = true
			continue
		}
		result = append(result, peer)
	}
	return result, filtered
}

func memberReferences(peers []Peer) map[string][]netip.Prefix {
	references := make(map[string][]netip.Prefix, len(peers))
	for _, peer := range peers {
		id, err := uuid.Parse(peer.NodeID)
		if err != nil {
			continue
		}
		prefix := netip.PrefixFrom(parseAddr(peer.VirtualIP), 32)
		for _, key := range []string{id.String(), "member:" + id.String(), "node:" + id.String()} {
			references[key] = append(references[key], prefix)
		}
	}
	return references
}

func (m *Manager) RecordDispatchFailure(ctx context.Context, nodeID uuid.UUID, cause error) error {
	var node models.Node
	if err := m.db.WithContext(ctx).Where("id = ?", nodeID).First(&node).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	message := "configuration delivery failed"
	if cause != nil {
		message = truncate(cause.Error(), 2000)
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.ConfigDispatchFailure
		result := tx.Where("target_type = ? AND target_id = ?", TargetNode, nodeID).First(&existing)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return tx.Create(&models.ConfigDispatchFailure{
				ID: uuid.New(), TenantID: node.TenantID, TargetType: TargetNode,
				TargetID: nodeID, LastError: message, Failures: 1,
				FailedAt: now, CreatedAt: now, UpdatedAt: now,
			}).Error
		}
		existing.LastError = message
		existing.Failures++
		existing.FailedAt = now
		existing.UpdatedAt = now
		return tx.Save(&existing).Error
	})
}

func (m *Manager) RecordDispatchSuccess(ctx context.Context, nodeID uuid.UUID) error {
	return m.db.WithContext(ctx).
		Where("target_type = ? AND target_id = ?", TargetNode, nodeID).
		Delete(&models.ConfigDispatchFailure{}).Error
}

func (m *Manager) Delivery(ctx context.Context, nodeID uuid.UUID, requestedVersion int, includePrivate bool) (Delivery, bool, error) {
	var node models.Node
	if err := m.db.WithContext(ctx).Where("id = ?", nodeID).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Delivery{}, false, service.ErrNotFound
		}
		return Delivery{}, false, fmt.Errorf("load delivery node: %w", err)
	}
	latest, err := m.Latest(ctx, TargetNode, nodeID)
	if errors.Is(err, service.ErrNotFound) {
		snapshot, buildErr := m.buildNodeConfig(ctx, m.db, node)
		if buildErr != nil {
			return Delivery{}, false, buildErr
		}
		configJSON, marshalErr := json.Marshal(snapshot)
		if marshalErr != nil {
			return Delivery{}, false, fmt.Errorf("marshal initial node config: %w", marshalErr)
		}
		latest = models.ConfigVersion{Version: 0, Config: configJSON}
	} else if err != nil {
		return Delivery{}, false, err
	}
	if requestedVersion > 0 && requestedVersion == latest.Version {
		return Delivery{Version: latest.Version}, true, nil
	}
	// Always serve the LATEST desired configuration to a client that is behind.
	// (Clients that are AHEAD — e.g. right after a rollback — also get the latest.)
	//
	// Returning the caller's own historical snapshot here would be a liveness bug:
	// a node lagging more than one version would apply that stale snapshot, store
	// its version, then request that same version forever and never converge.
	// Historical snapshots stay reachable via ListVersions/Rollback, which is where
	// they are actually needed.
	selected := latest
	var config NodeConfig
	if err := json.Unmarshal(selected.Config, &config); err != nil {
		return Delivery{}, false, fmt.Errorf("decode node config snapshot: %w", err)
	}
	delivery := Delivery{
		Version: selected.Version, Node: config.Node, Network: config.Network,
		ProxyRules: config.ProxyRules, ACL: config.ACL, Routes: config.Routes,
		PolicyFiltered: config.PolicyFiltered,
	}
	if delivery.ProxyRules == nil {
		delivery.ProxyRules = []ProxyRule{}
	}
	if delivery.ACL == nil {
		delivery.ACL = []ACLRule{}
	}
	if delivery.Routes == nil {
		delivery.Routes = []Route{}
	}
	if delivery.Network != nil {
		var network models.VirtualNetwork
		if err := m.db.WithContext(ctx).Where("id = ?", delivery.Network.ID).First(&network).Error; err == nil {
			secret, secretErr := m.crypto.Decrypt(network.Secret)
			if secretErr == nil {
				delivery.Network.NetworkSecret = secret
			}
			if network.PresharedKey != "" {
				if psk, pskErr := m.crypto.Decrypt(network.PresharedKey); pskErr == nil {
					delivery.Network.PresharedKey = psk
				}
			}
		}
	}
	privateKey := "(redacted)"
	if includePrivate {
		privateKey, err = m.crypto.Decrypt(node.PrivateKey)
		if err != nil {
			return Delivery{}, false, fmt.Errorf("decrypt delivery private key: %w", err)
		}
	}
	var meshNetwork mesh.Network
	if delivery.Network != nil {
		meshNetwork = mesh.Network{
			ID: delivery.Network.ID, Name: delivery.Network.Name, CIDR: delivery.Network.CIDR,
			Secret: delivery.Network.NetworkSecret, PresharedKey: delivery.Network.PresharedKey,
		}
		for _, peer := range delivery.Network.Peers {
			meshNetwork.Peers = append(meshNetwork.Peers, mesh.Peer{
				NodeID: peer.NodeID, PublicKey: peer.PublicKey, Endpoint: peer.Endpoint,
				VirtualIP: peer.VirtualIP, AllowedIPs: peer.AllowedIPs,
			})
		}
	}
	rendered, err := m.provider.RenderNodeConfig(ctx, mesh.Node{
		Name: config.Node.Name, PrivateKey: privateKey, PublicKey: node.PublicKey,
		VirtualIP: config.Node.VirtualIP, PublicEndpoint: config.Node.PublicEndpoint,
		Network: meshNetwork, PolicyFiltered: config.PolicyFiltered,
	})
	if err != nil && config.Network != nil {
		return Delivery{}, false, fmt.Errorf("render node WireGuard config: %w", err)
	}
	if err == nil {
		delivery.WireGuardConfig = string(rendered)
	}
	return delivery, false, nil
}

func (m *Manager) RenderExport(ctx context.Context, networkID uuid.UUID) ([]byte, error) {
	var network models.VirtualNetwork
	if err := m.db.WithContext(ctx).Where("id = ?", networkID).First(&network).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, fmt.Errorf("load export network: %w", err)
	}
	snapshot, err := m.buildNetworkConfig(ctx, m.db, network)
	if err != nil {
		return nil, err
	}
	secret, err := m.crypto.Decrypt(network.Secret)
	if err != nil {
		return nil, fmt.Errorf("decrypt export network secret: %w", err)
	}
	exportNetwork := snapshot.Network
	exportNetwork.NetworkSecret = secret
	return m.provider.RenderExport(ctx, mesh.Network{
		ID: exportNetwork.ID, Name: exportNetwork.Name, CIDR: exportNetwork.CIDR,
		Secret: exportNetwork.NetworkSecret,
		Peers:  peersForExport(exportNetwork.Peers),
	})
}

func peersForExport(peers []Peer) []mesh.Peer {
	result := make([]mesh.Peer, 0, len(peers))
	for _, peer := range peers {
		result = append(result, mesh.Peer{
			NodeID: peer.NodeID, PublicKey: peer.PublicKey, Endpoint: peer.Endpoint,
			VirtualIP: peer.VirtualIP, AllowedIPs: peer.AllowedIPs,
		})
	}
	return result
}

func (m *Manager) Latest(ctx context.Context, targetType string, targetID uuid.UUID) (models.ConfigVersion, error) {
	var version models.ConfigVersion
	err := m.db.WithContext(ctx).
		Where("target_type = ? AND target_id = ?", targetType, targetID).
		Order("version DESC").First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.ConfigVersion{}, service.ErrNotFound
	}
	if err != nil {
		return models.ConfigVersion{}, fmt.Errorf("load latest config version: %w", err)
	}
	m.setConfigVersionMetric(targetType, targetID, version.Version)
	return version, nil
}

func (m *Manager) GetVersion(ctx context.Context, targetType string, targetID uuid.UUID, version int) (models.ConfigVersion, error) {
	var item models.ConfigVersion
	err := m.db.WithContext(ctx).
		Where("target_type = ? AND target_id = ? AND version = ?", targetType, targetID, version).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.ConfigVersion{}, service.ErrNotFound
	}
	if err != nil {
		return models.ConfigVersion{}, fmt.Errorf("load config version: %w", err)
	}
	return item, nil
}

func (m *Manager) ListVersions(ctx context.Context, tenantID *uuid.UUID, targetType string, targetID uuid.UUID) (VersionList, error) {
	query := m.db.WithContext(ctx).Model(&models.ConfigVersion{}).
		Where("target_type = ? AND target_id = ?", targetType, targetID)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return VersionList{}, fmt.Errorf("count config versions: %w", err)
	}
	var rows []models.ConfigVersion
	if err := query.Order("version DESC").Find(&rows).Error; err != nil {
		return VersionList{}, fmt.Errorf("list config versions: %w", err)
	}
	items := make([]Version, 0, len(rows))
	for _, row := range rows {
		items = append(items, Version{
			ID: row.ID, TargetType: row.TargetType, TargetID: row.TargetID,
			Version: row.Version, Reason: row.Reason, Summary: row.Summary, CreatedAt: row.CreatedAt,
		})
	}
	return VersionList{Items: items, Total: total}, nil
}

func (m *Manager) Rollback(ctx context.Context, tenantID *uuid.UUID, targetType string, targetID uuid.UUID, sourceVersion int) (models.ConfigVersion, error) {
	if targetType != TargetNode && targetType != TargetNetwork && targetType != TargetProxy {
		return models.ConfigVersion{}, fmt.Errorf("%w: target_type must be node, network, or proxy", service.ErrInvalidInput)
	}
	source, err := m.GetVersion(ctx, targetType, targetID, sourceVersion)
	if err != nil {
		return models.ConfigVersion{}, err
	}
	if tenantID != nil && source.TenantID != *tenantID {
		return models.ConfigVersion{}, service.ErrNotFound
	}
	latest, err := m.Latest(ctx, targetType, targetID)
	if err != nil {
		return models.ConfigVersion{}, err
	}
	if sourceVersion == latest.Version {
		return source, nil
	}
	summary := datatypes.JSON(`{"source_version":` + fmt.Sprint(sourceVersion) + `,"rollback":true}`)
	var created models.ConfigVersion
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		next := latest.Version + 1
		created = models.ConfigVersion{
			ID: uuid.New(), TenantID: source.TenantID, TargetType: targetType, TargetID: targetID,
			Version: next, Config: source.Config, Reason: fmt.Sprintf("rollback to version %d", sourceVersion),
			Summary: summary,
		}
		if err := tx.Create(&created).Error; err != nil {
			if configUniqueViolation(err) {
				return errVersionRace
			}
			return fmt.Errorf("create rollback config version: %w", err)
		}
		return nil
	})
	if errors.Is(err, errVersionRace) {
		return models.ConfigVersion{}, service.ErrConflict
	}
	if err != nil {
		return models.ConfigVersion{}, err
	}
	m.setConfigVersionMetric(targetType, targetID, created.Version)
	return created, nil
}

func summarizeConfig(value any) (datatypes.JSON, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal config summary: %w", err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, fmt.Errorf("decode config summary: %w", err)
	}
	summary := map[string]any{}
	switch typed := generic.(type) {
	case map[string]any:
		for key, value := range typed {
			switch child := value.(type) {
			case []any:
				summary[key+"_count"] = len(child)
			case map[string]any:
				summary[key+"_count"] = len(child)
			}
		}
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("encode config summary: %w", err)
	}
	return data, nil
}

func (m *Manager) setConfigVersionMetric(targetType string, targetID uuid.UUID, version int) {
	if m.metrics != nil {
		m.metrics.SetConfigVersion(targetType, targetID.String(), version)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func parseAddr(value string) netip.Addr {
	addr, _ := netip.ParseAddr(value)
	return addr
}

func truncate(value string, size int) string {
	if len(value) <= size {
		return value
	}
	return value[:size]
}

func configUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") ||
		strings.Contains(message, "duplicate") ||
		strings.Contains(message, "constraint failed")
}
