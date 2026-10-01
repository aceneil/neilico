package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
)

type TenantService struct {
	db *gorm.DB
}

func NewTenantService(db *gorm.DB) *TenantService {
	return &TenantService{db: db}
}

type TenantInput struct {
	Name string `json:"name"`
	Plan string `json:"plan"`
}

func (s *TenantService) List(ctx context.Context) ([]models.Tenant, error) {
	var tenants []models.Tenant
	if err := s.db.WithContext(ctx).Order("created_at DESC").Find(&tenants).Error; err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	return tenants, nil
}

func (s *TenantService) Get(ctx context.Context, id uuid.UUID) (models.Tenant, error) {
	var tenant models.Tenant
	err := s.db.WithContext(ctx).First(&tenant, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Tenant{}, ErrNotFound
	}
	if err != nil {
		return models.Tenant{}, fmt.Errorf("get tenant: %w", err)
	}
	return tenant, nil
}

func (s *TenantService) Create(ctx context.Context, input TenantInput) (models.Tenant, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return models.Tenant{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if input.Plan == "" {
		input.Plan = "free"
	}
	tenant := models.Tenant{ID: uuid.New(), Name: input.Name, Plan: input.Plan}
	if err := s.db.WithContext(ctx).Create(&tenant).Error; err != nil {
		if isUniqueViolation(err) {
			return models.Tenant{}, ErrConflict
		}
		return models.Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return tenant, nil
}

func (s *TenantService) Update(ctx context.Context, id uuid.UUID, input TenantInput) (models.Tenant, error) {
	tenant, err := s.Get(ctx, id)
	if err != nil {
		return models.Tenant{}, err
	}
	if name := strings.TrimSpace(input.Name); name != "" {
		tenant.Name = name
	}
	if input.Plan != "" {
		tenant.Plan = input.Plan
	}
	if err := s.db.WithContext(ctx).Save(&tenant).Error; err != nil {
		if isUniqueViolation(err) {
			return models.Tenant{}, ErrConflict
		}
		return models.Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	return tenant, nil
}

func (s *TenantService) Delete(ctx context.Context, id uuid.UUID) error {
	counts := []struct {
		label string
		value *int64
		count int64
	}{
		{"users", new(int64), 0},
		{"nodes", new(int64), 0},
		{"domains", new(int64), 0},
		{"virtual_networks", new(int64), 0},
	}
	for _, item := range counts {
		table := item.label
		if err := s.db.WithContext(ctx).Table(table).Where("tenant_id = ?", id).Count(item.value).Error; err != nil {
			return fmt.Errorf("check tenant %s: %w", table, err)
		}
		if *item.value > 0 {
			return fmt.Errorf("%w: tenant still has %s", ErrConflict, table)
		}
	}
	result := s.db.WithContext(ctx).Delete(&models.Tenant{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete tenant: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
