package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/validation"
)

const (
	DomainStatusPending  = "pending"
	DomainStatusActive   = "active"
	DomainStatusDisabled = "disabled"
)

type DomainService struct {
	db *gorm.DB
}

func NewDomainService(db *gorm.DB) *DomainService { return &DomainService{db: db} }

type DomainInput struct {
	Domain string     `json:"domain"`
	CertID *uuid.UUID `json:"cert_id,omitempty"`
	Status string     `json:"status,omitempty"`
}

type DomainList struct {
	Items    []models.Domain `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

func (s *DomainService) List(ctx context.Context, tenantID *uuid.UUID, page, pageSize int) (DomainList, error) {
	query := s.db.WithContext(ctx).Model(&models.Domain{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return DomainList{}, fmt.Errorf("count domains: %w", err)
	}
	var items []models.Domain
	if err := query.Preload("Certificate").Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return DomainList{}, fmt.Errorf("list domains: %w", err)
	}
	return DomainList{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *DomainService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.Domain, error) {
	query := s.db.WithContext(ctx).Preload("Certificate").Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.Domain
	err := query.First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Domain{}, ErrNotFound
	}
	if err != nil {
		return models.Domain{}, fmt.Errorf("get domain: %w", err)
	}
	return item, nil
}

func (s *DomainService) Create(ctx context.Context, tenantID uuid.UUID, input DomainInput) (models.Domain, error) {
	domain, err := validation.Domain(input.Domain)
	if err != nil {
		return models.Domain{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	status := input.Status
	if status == "" {
		status = DomainStatusPending
	}
	if err := validateDomainStatus(status); err != nil {
		return models.Domain{}, err
	}
	if input.CertID != nil {
		if _, err := s.certificate(ctx, tenantID, *input.CertID); err != nil {
			return models.Domain{}, err
		}
		status = DomainStatusActive
	}
	item := models.Domain{
		ID:       uuid.New(),
		TenantID: tenantID,
		Domain:   domain,
		CertID:   input.CertID,
		Status:   status,
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return models.Domain{}, ErrConflict
		}
		return models.Domain{}, fmt.Errorf("create domain: %w", err)
	}
	return item, nil
}

func (s *DomainService) Update(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID, input DomainInput) (models.Domain, error) {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return models.Domain{}, err
	}
	if domain, domainErr := validation.Domain(input.Domain); domainErr != nil {
		return models.Domain{}, fmt.Errorf("%w: %v", ErrInvalidInput, domainErr)
	} else {
		item.Domain = domain
	}
	if input.CertID != nil {
		if _, err := s.certificate(ctx, item.TenantID, *input.CertID); err != nil {
			return models.Domain{}, err
		}
		item.CertID = input.CertID
		item.Status = DomainStatusActive
	} else {
		item.CertID = nil
		status := input.Status
		if status == "" {
			status = DomainStatusPending
		}
		if err := validateDomainStatus(status); err != nil {
			return models.Domain{}, err
		}
		item.Status = status
	}
	var conflict int64
	if err := s.db.WithContext(ctx).Model(&models.Domain{}).Where("domain = ? AND id <> ?", item.Domain, item.ID).Count(&conflict).Error; err != nil {
		return models.Domain{}, fmt.Errorf("check domain conflict: %w", err)
	}
	if conflict > 0 {
		return models.Domain{}, ErrConflict
	}
	if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
		if isUniqueViolation(err) {
			return models.Domain{}, ErrConflict
		}
		return models.Domain{}, fmt.Errorf("update domain: %w", err)
	}
	return item, nil
}

func (s *DomainService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.ProxyRule{}).Where("domain_id = ?", item.ID).Count(&count).Error; err != nil {
		return fmt.Errorf("check domain proxy rules: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%w: domain still has proxy rules", ErrConflict)
	}
	if err := s.db.WithContext(ctx).Delete(&models.Domain{}, "id = ?", item.ID).Error; err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	return nil
}

func (s *DomainService) certificate(ctx context.Context, tenantID, id uuid.UUID) (models.Certificate, error) {
	var item models.Certificate
	err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Certificate{}, ErrNotFound
	}
	if err != nil {
		return models.Certificate{}, fmt.Errorf("get certificate: %w", err)
	}
	return item, nil
}

func validateDomainStatus(status string) error {
	switch status {
	case DomainStatusPending, DomainStatusActive, DomainStatusDisabled:
		return nil
	default:
		return fmt.Errorf("%w: status must be pending, active, or disabled", ErrInvalidInput)
	}
}
