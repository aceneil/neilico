package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
)

type AuditLogFilter struct {
	TenantID *uuid.UUID
	UserID   *uuid.UUID
	Action   string
	Resource string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

type AuditLogList struct {
	Items    []models.AuditLog `json:"items"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

type AuditLogService struct {
	db *gorm.DB
}

func NewAuditLogService(db *gorm.DB) *AuditLogService {
	return &AuditLogService{db: db}
}

func (s *AuditLogService) List(ctx context.Context, filter AuditLogFilter) (AuditLogList, error) {
	query := s.db.WithContext(ctx).Model(&models.AuditLog{})
	if filter.TenantID != nil {
		query = query.Where("tenant_id = ?", *filter.TenantID)
	}
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.Resource != "" {
		query = query.Where("resource = ?", filter.Resource)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("created_at <= ?", *filter.To)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return AuditLogList{}, fmt.Errorf("count audit logs: %w", err)
	}
	items := make([]models.AuditLog, 0)
	err := query.
		Order("created_at DESC, id DESC").
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&items).Error
	if err != nil {
		return AuditLogList{}, fmt.Errorf("list audit logs: %w", err)
	}
	return AuditLogList{
		Items:    items,
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}
