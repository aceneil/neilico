package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/models"
)

func Bootstrap(ctx context.Context, db *gorm.DB, cfg config.Bootstrap) error {
	var count int64
	if err := db.WithContext(ctx).Model(&models.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	if strings.TrimSpace(cfg.AdminEmail) == "" || cfg.AdminPassword == "" || strings.TrimSpace(cfg.DefaultTenant) == "" {
		return fmt.Errorf("bootstrap admin_email, admin_password, and default_tenant are required")
	}
	passwordHash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tenant models.Tenant
		err := tx.Where("name = ?", strings.TrimSpace(cfg.DefaultTenant)).First(&tenant).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("find bootstrap tenant: %w", err)
		}
		if err == gorm.ErrRecordNotFound {
			tenant = models.Tenant{ID: uuid.New(), Name: strings.TrimSpace(cfg.DefaultTenant), Plan: "enterprise"}
			if err := tx.Create(&tenant).Error; err != nil {
				return fmt.Errorf("create bootstrap tenant: %w", err)
			}
		}
		admin := models.User{
			ID:           uuid.New(),
			TenantID:     tenant.ID,
			Email:        strings.ToLower(strings.TrimSpace(cfg.AdminEmail)),
			PasswordHash: passwordHash,
			Role:         auth.RolePlatformAdmin,
			Status:       "active",
		}
		if err := tx.Create(&admin).Error; err != nil {
			return fmt.Errorf("create bootstrap admin: %w", err)
		}
		return nil
	})
}
