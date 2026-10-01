package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/models"
)

const (
	NodeStatusOnline  = "online"
	NodeStatusOffline = "offline"
)

type NodeService struct {
	db      *gorm.DB
	timeout time.Duration
}

func NewNodeService(db *gorm.DB, heartbeatTimeout time.Duration) *NodeService {
	return &NodeService{db: db, timeout: heartbeatTimeout}
}

type NodeRegisterInput struct {
	Name      string   `json:"name"`
	VirtualIP *string  `json:"virtual_ip,omitempty"`
	PublicKey string   `json:"public_key"`
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	Version   string   `json:"version"`
	Tags      []string `json:"tags"`
}

type NodeRegisterOutput struct {
	NodeID     uuid.UUID `json:"node_id"`
	AgentToken string    `json:"agent_token"`
	TenantID   uuid.UUID `json:"tenant_id"`
	Status     string    `json:"status"`
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
	Version string `json:"version"`
}

func (s *NodeService) Register(ctx context.Context, tenantID uuid.UUID, input NodeRegisterInput) (NodeRegisterOutput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.PublicKey = strings.TrimSpace(input.PublicKey)
	input.OS = strings.TrimSpace(input.OS)
	input.Arch = strings.TrimSpace(input.Arch)
	input.Version = strings.TrimSpace(input.Version)
	if input.Name == "" || input.PublicKey == "" || input.OS == "" || input.Arch == "" {
		return NodeRegisterOutput{}, fmt.Errorf("%w: name, public_key, os, and arch are required", ErrInvalidInput)
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
		PublicKey:      input.PublicKey,
		VirtualIP:      virtualIP,
		OS:             input.OS,
		Arch:           input.Arch,
		Version:        input.Version,
		Status:         NodeStatusOffline,
		Tags:           datatypes.JSONSlice[string](tags),
		AgentTokenHash: tokenHash,
	}
	if err := s.db.WithContext(ctx).Create(&node).Error; err != nil {
		return NodeRegisterOutput{}, fmt.Errorf("register node: %w", err)
	}
	return NodeRegisterOutput{
		NodeID:     node.ID,
		AgentToken: plain,
		TenantID:   node.TenantID,
		Status:     node.Status,
	}, nil
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
	return node, nil
}

func (s *NodeService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	node, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(&models.Node{}, "id = ?", node.ID).Error; err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	return nil
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
