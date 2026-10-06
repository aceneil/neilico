package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service/cert"
	"neilico/control-plane/internal/service/mesh/wireguard"
	"neilico/control-plane/pkg/capabilities"
)

const (
	NodeStatusOnline  = "online"
	NodeStatusOffline = "offline"
)

type NodeService struct {
	db      *gorm.DB
	timeout time.Duration
	crypto  *cert.Crypto
}

func NewNodeService(db *gorm.DB, heartbeatTimeout time.Duration, keyCrypto ...*cert.Crypto) *NodeService {
	var keyCipher *cert.Crypto
	if len(keyCrypto) > 0 {
		keyCipher = keyCrypto[0]
	}
	return &NodeService{db: db, timeout: heartbeatTimeout, crypto: keyCipher}
}

func (s *NodeService) ConfigureKeyCrypto(keyCrypto *cert.Crypto) {
	if s.crypto == nil {
		s.crypto = keyCrypto
	}
}

type NodeRegisterInput struct {
	Name         string                     `json:"name"`
	VirtualIP    *string                    `json:"virtual_ip,omitempty"`
	PublicKey    string                     `json:"public_key"`
	OS           string                     `json:"os"`
	Arch         string                     `json:"arch"`
	Version      string                     `json:"version"`
	Tags         []string                   `json:"tags"`
	Capabilities *capabilities.Capabilities `json:"capabilities,omitempty"`
}

type NodeRegisterOutput struct {
	NodeID     uuid.UUID `json:"node_id"`
	AgentToken string    `json:"agent_token"`
	TenantID   uuid.UUID `json:"tenant_id"`
	Status     string    `json:"status"`
	PublicKey  string    `json:"public_key"`
	PrivateKey string    `json:"private_key,omitempty"`
}

func resolveCapabilities(value *capabilities.Capabilities) (capabilities.Capabilities, error) {
	if value == nil {
		return capabilities.Unknown("尚未上报"), nil
	}
	resolved := *value
	resolved.Reason = strings.TrimSpace(resolved.Reason)
	if err := resolved.Validate(); err != nil {
		return capabilities.Capabilities{}, err
	}
	return resolved, nil
}

func validateVirtualIP(value string) error {
	host := value
	if prefix, err := netip.ParsePrefix(value); err == nil {
		host = prefix.Addr().String()
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return errors.New("virtual_ip must be an IP address")
	}
	return nil
}

type HeartbeatInput struct {
	Version      string                     `json:"version"`
	Capabilities *capabilities.Capabilities `json:"capabilities,omitempty"`
}

func (s *NodeService) Register(ctx context.Context, tenantID uuid.UUID, input NodeRegisterInput) (NodeRegisterOutput, error) {
	return s.RegisterTx(ctx, s.db, tenantID, input)
}

func (s *NodeService) RegisterTx(ctx context.Context, tx *gorm.DB, tenantID uuid.UUID, input NodeRegisterInput) (NodeRegisterOutput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.OS = strings.TrimSpace(input.OS)
	input.Arch = strings.TrimSpace(input.Arch)
	input.Version = strings.TrimSpace(input.Version)
	caps, err := resolveCapabilities(input.Capabilities)
	if err != nil {
		return NodeRegisterOutput{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.Name == "" || input.OS == "" || input.Arch == "" {
		return NodeRegisterOutput{}, fmt.Errorf("%w: name, os, and arch are required", ErrInvalidInput)
	}
	privateKey, publicKey, err := wireguard.GenerateKeyPair()
	if err != nil {
		return NodeRegisterOutput{}, err
	}
	encryptedPrivateKey, err := s.encryptPrivateKey(privateKey)
	if err != nil {
		return NodeRegisterOutput{}, err
	}
	plain, tokenHash, err := auth.GenerateAgentToken()
	if err != nil {
		return NodeRegisterOutput{}, err
	}
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	var virtualIP *string
	if input.VirtualIP != nil {
		value := strings.TrimSpace(*input.VirtualIP)
		if err := validateVirtualIP(value); err != nil {
			return NodeRegisterOutput{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		virtualIP = &value
	}
	node := models.Node{
		ID:             uuid.New(),
		TenantID:       tenantID,
		Name:           input.Name,
		PublicKey:      publicKey,
		PrivateKey:     encryptedPrivateKey,
		VirtualIP:      virtualIP,
		OS:             input.OS,
		Arch:           input.Arch,
		Version:        input.Version,
		Status:         NodeStatusOffline,
		Tags:           datatypes.JSONSlice[string](tags),
		Capabilities:   datatypes.NewJSONType(caps),
		AgentTokenHash: tokenHash,
	}
	if err := tx.WithContext(ctx).Create(&node).Error; err != nil {
		return NodeRegisterOutput{}, fmt.Errorf("register node: %w", err)
	}
	return NodeRegisterOutput{
		NodeID:     node.ID,
		AgentToken: plain,
		TenantID:   node.TenantID,
		Status:     node.Status,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}, nil
}

func (s *NodeService) encryptPrivateKey(value string) (string, error) {
	if s.crypto == nil {
		return "", errors.New("node private key encryption is unavailable")
	}
	encrypted, err := s.crypto.Encrypt(value)
	if err != nil {
		return "", fmt.Errorf("encrypt node private key: %w", err)
	}
	return encrypted, nil
}

func (s *NodeService) DecryptPrivateKey(node models.Node) (string, error) {
	if s.crypto == nil || node.PrivateKey == "" {
		return "", errors.New("node private key is unavailable")
	}
	plaintext, err := s.crypto.Decrypt(node.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("decrypt node private key: %w", err)
	}
	return plaintext, nil
}

type NodeKeyRotateOutput struct {
	NodeID     uuid.UUID `json:"node_id"`
	PublicKey  string    `json:"public_key"`
	PrivateKey string    `json:"private_key"`
}

func (s *NodeService) RotateKey(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (NodeKeyRotateOutput, error) {
	node, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return NodeKeyRotateOutput{}, err
	}
	privateKey, publicKey, err := wireguard.GenerateKeyPair()
	if err != nil {
		return NodeKeyRotateOutput{}, err
	}
	encrypted, err := s.encryptPrivateKey(privateKey)
	if err != nil {
		return NodeKeyRotateOutput{}, err
	}
	result := s.db.WithContext(ctx).Model(&models.Node{}).Where("id = ?", node.ID).Updates(map[string]any{
		"public_key":  publicKey,
		"private_key": encrypted,
	})
	if result.Error != nil {
		return NodeKeyRotateOutput{}, fmt.Errorf("rotate node key: %w", result.Error)
	}
	return NodeKeyRotateOutput{NodeID: node.ID, PublicKey: publicKey, PrivateKey: privateKey}, nil
}

func (s *NodeService) AuthenticateNode(ctx context.Context, nodeID uuid.UUID, agentToken string) (models.Node, error) {
	if agentToken == "" {
		return models.Node{}, ErrForbidden
	}
	var node models.Node
	err := s.db.WithContext(ctx).
		Where("id = ? AND agent_token_hash = ?", nodeID, auth.HashAgentToken(agentToken)).
		First(&node).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Node{}, ErrForbidden
	}
	if err != nil {
		return models.Node{}, fmt.Errorf("authenticate node: %w", err)
	}
	return node, nil
}

type NetworkReportInput struct {
	PublicEndpoint string `json:"public_endpoint"`
	// LocalAddresses：agent 上报的本机内网地址（CIDR）。见 models.Node.LocalAddresses。
	LocalAddresses []string `json:"local_addresses"`
	// ListenPort：agent 自己的 WireGuard 监听端口（对端拼内网 endpoint 时使用）。
	ListenPort int `json:"listen_port"`
}

// defaultWireGuardListenPort 是 agent 未上报/上报非法端口时的兜底监听端口。
const defaultWireGuardListenPort = 51820

// maxReportedLocalAddresses 限制上报条数，避免超长请求体撑爆 jsonb 字段。
const maxReportedLocalAddresses = 8

// normalizeLocalAddresses 校验并规整 agent 上报的内网地址：
// 只接受可解析的 IP/CIDR，跳过回环与链路本地，去重，最多保留 maxReportedLocalAddresses 条。
// 无法识别的条目直接丢弃（不让脏数据进库）。
func normalizeLocalAddresses(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(trimmed); err == nil {
			address := prefix.Addr()
			if !usableReportedAddress(address) {
				continue
			}
			// 保留上报的**具体地址 + 前缀长度**（如 192.168.1.10/24）：对端要用这个
			// 地址建隧道；若掩码成网段会变成 192.168.1.0，是个没法拨号的网络地址。
			trimmed = prefix.String()
		} else if address, err := netip.ParseAddr(trimmed); err == nil {
			if !usableReportedAddress(address) {
				continue
			}
			trimmed = address.String()
		} else {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
		if len(normalized) >= maxReportedLocalAddresses {
			break
		}
	}
	return normalized
}

func usableReportedAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsUnspecified()
}

func (s *NodeService) NetworkReport(ctx context.Context, nodeID uuid.UUID, agentToken string, input NetworkReportInput) (models.Node, error) {
	node, err := s.AuthenticateNode(ctx, nodeID, agentToken)
	if err != nil {
		return models.Node{}, err
	}
	endpoint := strings.TrimSpace(input.PublicEndpoint)
	if endpoint != "" {
		host, port, splitErr := net.SplitHostPort(endpoint)
		if splitErr != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return models.Node{}, fmt.Errorf("%w: public_endpoint must use host:port syntax", ErrInvalidInput)
		}
		portNumber, portErr := strconv.Atoi(port)
		if portErr != nil || portNumber < 1 || portNumber > 65535 {
			return models.Node{}, fmt.Errorf("%w: public_endpoint port must be between 1 and 65535", ErrInvalidInput)
		}
	}
	now := time.Now().UTC()
	node.LastSeen = &now
	node.Status = NodeStatusOnline
	if endpoint == "" {
		node.PublicEndpoint = nil
	} else {
		node.PublicEndpoint = &endpoint
	}
	node.LocalAddresses = datatypes.JSONSlice[string](normalizeLocalAddresses(input.LocalAddresses))
	if input.ListenPort >= 1 && input.ListenPort <= 65535 {
		node.ListenPort = input.ListenPort
	} else if node.ListenPort < 1 {
		node.ListenPort = defaultWireGuardListenPort
	}
	if err := s.db.WithContext(ctx).Save(&node).Error; err != nil {
		return models.Node{}, fmt.Errorf("record network report: %w", err)
	}
	return node, nil
}

func (s *NodeService) Heartbeat(ctx context.Context, nodeID uuid.UUID, agentToken string, input HeartbeatInput) (models.Node, error) {
	if agentToken == "" {
		return models.Node{}, ErrForbidden
	}
	var node models.Node
	err := s.db.WithContext(ctx).
		Where("id = ? AND agent_token_hash = ?", nodeID, auth.HashAgentToken(agentToken)).
		First(&node).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Node{}, ErrForbidden
	}
	if err != nil {
		return models.Node{}, fmt.Errorf("authenticate node: %w", err)
	}
	now := time.Now().UTC()
	node.LastSeen = &now
	node.Status = NodeStatusOnline
	if version := strings.TrimSpace(input.Version); version != "" {
		node.Version = version
	}
	if input.Capabilities != nil {
		if caps, capErr := resolveCapabilities(input.Capabilities); capErr == nil {
			node.Capabilities = datatypes.NewJSONType(caps)
		}
	}
	if err := s.db.WithContext(ctx).Save(&node).Error; err != nil {
		return models.Node{}, fmt.Errorf("record heartbeat: %w", err)
	}
	return node, nil
}

type NodeListFilter struct {
	Status   string
	Tag      string
	Page     int
	PageSize int
}

type NodeList struct {
	Items []models.Node `json:"items"`
	Total int64         `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"page_size"`
}

func (s *NodeService) List(ctx context.Context, tenantID *uuid.UUID, filter NodeListFilter) (NodeList, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&models.Node{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var nodes []models.Node
	if err := query.Order("created_at DESC").Find(&nodes).Error; err != nil {
		return NodeList{}, fmt.Errorf("list nodes: %w", err)
	}
	nodePointers := make([]*models.Node, len(nodes))
	for index := range nodes {
		nodePointers[index] = &nodes[index]
	}
	if err := s.attachMembership(ctx, nodePointers...); err != nil {
		return NodeList{}, err
	}
	if filter.Tag != "" {
		filtered := nodes[:0]
		for _, node := range nodes {
			for _, tag := range node.Tags {
				if tag == filter.Tag {
					filtered = append(filtered, node)
					break
				}
			}
		}
		nodes = filtered
	}
	total := int64(len(nodes))
	start := (filter.Page - 1) * filter.PageSize
	if start >= len(nodes) {
		nodes = []models.Node{}
	} else {
		end := start + filter.PageSize
		if end > len(nodes) {
			end = len(nodes)
		}
		nodes = nodes[start:end]
	}
	return NodeList{Items: nodes, Total: total, Page: filter.Page, Size: filter.PageSize}, nil
}

func (s *NodeService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.Node, error) {
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
		return models.Node{}, fmt.Errorf("get node: %w", err)
	}
	if err := s.attachMembership(ctx, &node); err != nil {
		return models.Node{}, err
	}
	return node, nil
}

type nodeMembership struct {
	NodeID    uuid.UUID `gorm:"column:node_id"`
	NetworkID uuid.UUID `gorm:"column:network_id"`
	VirtualIP string    `gorm:"column:virtual_ip"`
}

func (s *NodeService) attachMembership(ctx context.Context, nodes ...*models.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	var memberships []nodeMembership
	if err := s.db.WithContext(ctx).Model(&models.NetworkMember{}).
		Select("node_id, network_id, virtual_ip").
		Where("node_id IN ?", ids).
		Order("joined_at DESC, id DESC").
		Find(&memberships).Error; err != nil {
		return fmt.Errorf("load node network membership: %w", err)
	}
	byNode := make(map[uuid.UUID]nodeMembership, len(memberships))
	for _, membership := range memberships {
		if _, exists := byNode[membership.NodeID]; !exists {
			byNode[membership.NodeID] = membership
		}
	}
	// 老数据（AutoMigrate 用 default '{}' 补出的列）里 capabilities 是空对象；
	// 在读取边界补成明确状态，避免界面/接口把它显示成空白。
	// 同时算出「有效 Mesh 状态」与心跳时效：有效状态以 tunnel 为权威信号
	// （tunnel 不可用 → Mesh 不可用），并对自相矛盾的 capabilities 取更悲观者，
	// 绝不让 mesh=ready 把隧道故障掩盖成绿色就绪。
	now := time.Now().UTC()
	for _, node := range nodes {
		caps := node.Capabilities.Data().Normalize()
		node.Capabilities = datatypes.NewJSONType(caps)
		node.EffectiveMesh = caps.EffectiveMesh()
		node.CapabilitiesNote = caps.Note()
		node.HeartbeatStale = IsHeartbeatExpired(node.LastSeen, s.timeout, now)
	}
	for _, node := range nodes {
		membership, ok := byNode[node.ID]
		if !ok {
			continue
		}
		virtualIP := membership.VirtualIP
		node.VirtualIP = &virtualIP
		networkID := membership.NetworkID
		node.NetworkID = &networkID
	}
	return nil
}

func (s *NodeService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	node, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	// 指向 nodes 的外键里，network_members / subnet_routes / traffic_logs 都是 RESTRICT
	// （node_enrollments 是 CASCADE）。所以直接删节点会撞外键：
	//   update or delete on table "nodes" violates foreign key constraint "fk_network_members_node"
	// 真机表现为 HTTP 500（实测）。这些行都是「该设备自身的附着状态与遥测」，随设备一起清理；
	// 审计留痕在 audit_logs（不依赖 nodes 外键），因此历史不受影响。
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		children := []any{
			&models.NetworkMember{}, // 退出所有虚拟网络
			&models.SubnetRoute{},   // 撤销该节点的子网路由
			&models.TrafficLog{},    // 该设备自身的流量遥测
		}
		for _, child := range children {
			if err := tx.Where("node_id = ?", node.ID).Delete(child).Error; err != nil {
				return fmt.Errorf("detach node reference (%T): %w", child, err)
			}
		}
		if err := tx.Delete(&models.Node{}, "id = ?", node.ID).Error; err != nil {
			return fmt.Errorf("delete node: %w", err)
		}
		return nil
	})
}

func IsHeartbeatExpired(lastSeen *time.Time, timeout time.Duration, now time.Time) bool {
	return lastSeen == nil || now.Sub(lastSeen.UTC()) > timeout
}

type NodeSweeper struct {
	db      *gorm.DB
	timeout time.Duration
	logger  *slog.Logger
}

func NewNodeSweeper(db *gorm.DB, timeout time.Duration, logger *slog.Logger) *NodeSweeper {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeSweeper{db: db, timeout: timeout, logger: logger}
}

func (s *NodeSweeper) RunOnce(ctx context.Context) (int64, error) {
	cutoff := time.Now().UTC().Add(-s.timeout)
	result := s.db.WithContext(ctx).
		Model(&models.Node{}).
		Where("last_seen IS NOT NULL AND last_seen < ? AND status <> ?", cutoff, NodeStatusOffline).
		Update("status", NodeStatusOffline)
	if result.Error != nil {
		return 0, fmt.Errorf("sweep expired nodes: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (s *NodeSweeper) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if count, err := s.RunOnce(ctx); err != nil {
				s.logger.Error("node sweeper failed", "error", err)
			} else if count > 0 {
				s.logger.Info("marked expired nodes offline", "count", count)
			}
		}
	}
}
