package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/validation"
)

// StreamRuleService 管理「端口转发」规则（任意 TCP/UDP 的地址:端口打通）。
//
// 与 ProxyRuleService 并列：代理规则管带域名的 HTTP/HTTPS，本服务管其余协议。
// 监听端口被限制在「容器已对外发布」的端口段内（minPort/maxPort），否则规则会
// 显示运行中却从外部连不上。
type StreamRuleService struct {
	db      *gorm.DB
	minPort int
	maxPort int
}

func NewStreamRuleService(db *gorm.DB, minPort, maxPort int) *StreamRuleService {
	// 未配置端口段时给一个安全默认：只允许发布段内的非特权端口。
	if minPort == 0 && maxPort == 0 {
		minPort, maxPort = 20000, 20019
	}
	return &StreamRuleService{db: db, minPort: minPort, maxPort: maxPort}
}

// PortRange 返回允许的监听端口区间（供 UI 提示）。
func (s *StreamRuleService) PortRange() (int, int) { return s.minPort, s.maxPort }

type StreamRuleInput struct {
	Name        string   `json:"name"`
	Protocol    string   `json:"protocol,omitempty"`
	ListenPort  int      `json:"listen_port"`
	TargetType  string   `json:"target_type"`
	Target      string   `json:"target"`
	IPWhitelist []string `json:"ip_whitelist,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
}

type StreamRuleList struct {
	Items    []models.StreamRule `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

// ResolvedStream 是喂给转发引擎的运行视图：目标已从 node 解析成虚拟 IP。
type ResolvedStream struct {
	Rule models.StreamRule
	Host string
	Port int
}

func (s *StreamRuleService) List(ctx context.Context, tenantID *uuid.UUID, page, pageSize int) (StreamRuleList, error) {
	query := s.db.WithContext(ctx).Model(&models.StreamRule{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return StreamRuleList{}, fmt.Errorf("count stream rules: %w", err)
	}
	var items []models.StreamRule
	if err := query.Order("listen_port ASC, protocol ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return StreamRuleList{}, fmt.Errorf("list stream rules: %w", err)
	}
	return StreamRuleList{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *StreamRuleService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.StreamRule, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.StreamRule
	err := query.First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.StreamRule{}, ErrNotFound
	}
	if err != nil {
		return models.StreamRule{}, fmt.Errorf("get stream rule: %w", err)
	}
	return item, nil
}

func (s *StreamRuleService) Create(ctx context.Context, tenantID uuid.UUID, input StreamRuleInput) (models.StreamRule, error) {
	item := models.StreamRule{ID: uuid.New(), TenantID: tenantID, Enabled: true}
	if err := s.apply(ctx, &item, input, true); err != nil {
		return models.StreamRule{}, err
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return models.StreamRule{}, fmt.Errorf("create stream rule: %w", err)
	}
	return item, nil
}

func (s *StreamRuleService) Update(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID, input StreamRuleInput) (models.StreamRule, error) {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return models.StreamRule{}, err
	}
	if err := s.apply(ctx, &item, input, false); err != nil {
		return models.StreamRule{}, err
	}
	if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
		return models.StreamRule{}, fmt.Errorf("update stream rule: %w", err)
	}
	return item, nil
}

func (s *StreamRuleService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(&models.StreamRule{}, "id = ?", item.ID).Error; err != nil {
		return fmt.Errorf("delete stream rule: %w", err)
	}
	return nil
}

// Resolved 返回所有启用规则的运行视图（node 目标解析成该节点的虚拟 IP）。
// 单条规则解析失败不影响其余规则：它出现在 problems 里，由调用方在状态中体现为 error。
func (s *StreamRuleService) Resolved(ctx context.Context, tenantID *uuid.UUID) ([]ResolvedStream, map[uuid.UUID]string) {
	query := s.db.WithContext(ctx).Where("enabled = ?", true).Order("listen_port ASC")
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var rules []models.StreamRule
	if err := query.Find(&rules).Error; err != nil {
		return nil, map[uuid.UUID]string{}
	}
	resolved := make([]ResolvedStream, 0, len(rules))
	problems := make(map[uuid.UUID]string)
	for _, rule := range rules {
		host, port, err := s.resolveTarget(ctx, rule)
		if err != nil {
			problems[rule.ID] = err.Error()
			continue
		}
		resolved = append(resolved, ResolvedStream{Rule: rule, Host: host, Port: port})
	}
	return resolved, problems
}

func (s *StreamRuleService) resolveTarget(ctx context.Context, rule models.StreamRule) (string, int, error) {
	_, rawPort, err := net.SplitHostPort(rule.Target)
	if err != nil {
		return "", 0, fmt.Errorf("target must be host:port: %w", err)
	}
	port, err := net.LookupPort("tcp", rawPort)
	if err != nil {
		return "", 0, fmt.Errorf("invalid target port %q", rawPort)
	}
	if rule.TargetType != "node" {
		return strings.TrimSpace(rule.Target[:len(rule.Target)-len(rawPort)-1]), port, nil
	}
	host, _, err := net.SplitHostPort(rule.Target)
	if err != nil {
		return "", 0, err
	}
	nodeID, err := uuid.Parse(host)
	if err != nil {
		return "", 0, fmt.Errorf("node target must start with a node UUID: %w", err)
	}
	var member models.NetworkMember
	err = s.db.WithContext(ctx).
		Where("node_id = ?", nodeID).
		Order("joined_at DESC, id ASC").
		First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", 0, fmt.Errorf("node %s has no virtual IP assigned", nodeID)
	}
	if err != nil {
		return "", 0, fmt.Errorf("resolve node virtual IP: %w", err)
	}
	virtualIP := strings.TrimSpace(member.VirtualIP)
	if parsed, _, err := net.ParseCIDR(virtualIP); err == nil {
		virtualIP = parsed.String()
	} else if addr := net.ParseIP(virtualIP); addr != nil {
		virtualIP = addr.String()
	} else {
		return "", 0, fmt.Errorf("node %s has invalid virtual IP %q", nodeID, member.VirtualIP)
	}
	return virtualIP, port, nil
}

func (s *StreamRuleService) apply(ctx context.Context, item *models.StreamRule, input StreamRuleInput, creating bool) error {
	name, err := validation.StreamName(input.Name)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	protocol, err := validation.StreamProtocol(input.Protocol)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if _, err := validation.StreamListenPort(input.ListenPort, s.minPort, s.maxPort); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := validation.StreamTarget(input.TargetType, input.Target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	whitelist, err := validation.StreamIPWhitelist(input.IPWhitelist)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	// (protocol, listen_port) 唯一：同一端口号允许 tcp/udp 各一条（如 DNS），但同协议不可重复。
	var existing models.StreamRule
	query := s.db.WithContext(ctx).
		Where("protocol = ? AND listen_port = ?", protocol, input.ListenPort)
	if !creating {
		query = query.Where("id <> ?", item.ID)
	}
	if err := query.First(&existing).Error; err == nil {
		return fmt.Errorf("%w: %s port %d is already used by rule %q", ErrConflict, protocol, input.ListenPort, existing.Name)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("check stream port: %w", err)
	}

	item.Name = name
	item.Protocol = protocol
	item.ListenPort = input.ListenPort
	item.TargetType = input.TargetType
	item.Target = strings.TrimSpace(input.Target)
	item.IPWhitelist = datatypes.JSONSlice[string](whitelist)
	if input.Enabled != nil {
		item.Enabled = *input.Enabled
	}
	return nil
}
