package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/models"
)

const MaxTrafficBatch = 1000

type TrafficInput struct {
	Direction string    `json:"direction"`
	Bytes     int64     `json:"bytes"`
	Protocol  string    `json:"protocol"`
	Peer      string    `json:"peer"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type TrafficService struct {
	db *gorm.DB
}

func NewTrafficService(db *gorm.DB) *TrafficService { return &TrafficService{db: db} }

func (s *TrafficService) Record(ctx context.Context, nodeID uuid.UUID, agentToken string, entries []TrafficInput) (int, error) {
	if agentToken == "" || len(entries) == 0 || len(entries) > MaxTrafficBatch {
		return 0, ErrInvalidInput
	}
	var node models.Node
	err := s.db.WithContext(ctx).Where("id = ? AND agent_token_hash = ?", nodeID, auth.HashAgentToken(agentToken)).First(&node).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrForbidden
	}
	if err != nil {
		return 0, fmt.Errorf("authenticate traffic node: %w", err)
	}
	logs := make([]models.TrafficLog, 0, len(entries))
	now := time.Now().UTC()
	for _, entry := range entries {
		if entry.Direction != "in" && entry.Direction != "out" {
			return 0, fmt.Errorf("%w: traffic direction must be in or out", ErrInvalidInput)
		}
		if entry.Bytes < 0 || strings.TrimSpace(entry.Protocol) == "" || strings.TrimSpace(entry.Peer) == "" {
			return 0, fmt.Errorf("%w: traffic bytes, protocol, and peer are required", ErrInvalidInput)
		}
		createdAt := entry.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		logs = append(logs, models.TrafficLog{
			ID:        uuid.New(),
			TenantID:  node.TenantID,
			NodeID:    node.ID,
			Direction: entry.Direction,
			Bytes:     entry.Bytes,
			Protocol:  strings.TrimSpace(entry.Protocol),
			Peer:      strings.TrimSpace(entry.Peer),
			CreatedAt: createdAt.UTC(),
		})
	}
	if err := s.db.WithContext(ctx).Create(&logs).Error; err != nil {
		return 0, fmt.Errorf("create traffic logs: %w", err)
	}
	return len(logs), nil
}

type TrafficFilter struct {
	NodeID *uuid.UUID
	From   *time.Time
	To     *time.Time
	Page   int
	Size   int
}

type TrafficList struct {
	Items    []models.TrafficLog `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

func (s *TrafficService) List(ctx context.Context, tenantID *uuid.UUID, filter TrafficFilter) (TrafficList, error) {
	query := s.db.WithContext(ctx).Model(&models.TrafficLog{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	if filter.NodeID != nil {
		query = query.Where("node_id = ?", *filter.NodeID)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("created_at <= ?", *filter.To)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return TrafficList{}, fmt.Errorf("count traffic logs: %w", err)
	}
	var items []models.TrafficLog
	if err := query.Order("created_at DESC").Offset((filter.Page - 1) * filter.Size).Limit(filter.Size).Find(&items).Error; err != nil {
		return TrafficList{}, fmt.Errorf("list traffic logs: %w", err)
	}
	return TrafficList{Items: items, Total: total, Page: filter.Page, PageSize: filter.Size}, nil
}
