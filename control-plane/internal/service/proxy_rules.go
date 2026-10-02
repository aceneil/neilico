package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/validation"
)

type ProxyRuleService struct {
	db *gorm.DB
}

func NewProxyRuleService(db *gorm.DB) *ProxyRuleService { return &ProxyRuleService{db: db} }

type ProxyRuleInput struct {
	DomainID                   uuid.UUID            `json:"domain_id"`
	Path                       string               `json:"path,omitempty"`
	TargetType                 string               `json:"target_type"`
	Target                     string               `json:"target"`
	AccessControl              models.AccessControl `json:"access_control"`
	UpstreamScheme             string               `json:"upstream_scheme,omitempty"`
	UpstreamInsecureSkipVerify bool                 `json:"upstream_insecure_skip_verify,omitempty"`
	UpstreamCAFile             string               `json:"upstream_ca_file,omitempty"`
	Enabled                    *bool                `json:"enabled,omitempty"`
}

type ProxyRuleList struct {
	Items    []models.ProxyRule `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

func (s *ProxyRuleService) List(ctx context.Context, tenantID *uuid.UUID, page, pageSize int) (ProxyRuleList, error) {
	query := s.db.WithContext(ctx).Model(&models.ProxyRule{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return ProxyRuleList{}, fmt.Errorf("count proxy rules: %w", err)
	}
	var items []models.ProxyRule
	if err := query.Preload("Domain").Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return ProxyRuleList{}, fmt.Errorf("list proxy rules: %w", err)
	}
	return ProxyRuleList{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *ProxyRuleService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.ProxyRule, error) {
	query := s.db.WithContext(ctx).Preload("Domain").Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.ProxyRule
	err := query.First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.ProxyRule{}, ErrNotFound
	}
	if err != nil {
		return models.ProxyRule{}, fmt.Errorf("get proxy rule: %w", err)
	}
	return item, nil
}

func (s *ProxyRuleService) Create(ctx context.Context, tenantID uuid.UUID, input ProxyRuleInput) (models.ProxyRule, error) {
	item := models.ProxyRule{ID: uuid.New(), TenantID: tenantID, Enabled: true}
	if err := s.apply(ctx, &item, input, true); err != nil {
		return models.ProxyRule{}, err
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return models.ProxyRule{}, fmt.Errorf("create proxy rule: %w", err)
	}
	return item, nil
}

func (s *ProxyRuleService) Update(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID, input ProxyRuleInput) (models.ProxyRule, error) {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return models.ProxyRule{}, err
	}
	if err := s.apply(ctx, &item, input, false); err != nil {
		return models.ProxyRule{}, err
	}
	if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
		return models.ProxyRule{}, fmt.Errorf("update proxy rule: %w", err)
	}
	return item, nil
}

func (s *ProxyRuleService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(&models.ProxyRule{}, "id = ?", item.ID).Error; err != nil {
		return fmt.Errorf("delete proxy rule: %w", err)
	}
	return nil
}

func (s *ProxyRuleService) apply(ctx context.Context, item *models.ProxyRule, input ProxyRuleInput, creating bool) error {
	if input.DomainID != uuid.Nil {
		var domain models.Domain
		err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", input.DomainID, item.TenantID).First(&domain).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get proxy rule domain: %w", err)
		}
		item.DomainID = domain.ID
	} else if creating {
		return fmt.Errorf("%w: domain_id is required", ErrInvalidInput)
	}
	path, err := validation.ProxyPath(input.Path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	item.Path = path
	if _, _, err := validation.Target(input.TargetType, input.Target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	item.TargetType = input.TargetType
	item.Target = input.Target
	scheme := strings.TrimSpace(input.UpstreamScheme)
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: upstream_scheme must be http or https", ErrInvalidInput)
	}
	item.UpstreamScheme = scheme
	item.UpstreamInsecureSkipVerify = input.UpstreamInsecureSkipVerify
	item.UpstreamCAFile = strings.TrimSpace(input.UpstreamCAFile)
	if scheme != "https" && (item.UpstreamInsecureSkipVerify || item.UpstreamCAFile != "") {
		return fmt.Errorf("%w: upstream TLS options require upstream_scheme=https", ErrInvalidInput)
	}
	access, err := validation.AccessControl(input.AccessControl)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	item.AccessControl = access
	if input.Enabled != nil {
		item.Enabled = *input.Enabled
	}
	return nil
}
