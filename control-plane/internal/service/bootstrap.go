package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/models"
)

// Bootstrap 在「库里还没有任何账号」时，用 env 里配置的管理员凭据种入第一个平台管理员，
// 保证 env 里那串强密码仍可用于首次进入。
//
// 与「首次登入=注册」流程共存：若 env 未配置管理员邮箱/密码，这里不再报错退出，
// 而是直接返回 —— 此时 GET /api/v1/setup/status 会报告系统未初始化，
// POST /api/v1/setup/register 允许创建第一个管理员账号。
// 一旦库里已有账号（无论来自 env 引导还是注册），后续注册一律 409。
func Bootstrap(ctx context.Context, db *gorm.DB, cfg config.Bootstrap) error {
	var count int64
	if err := db.WithContext(ctx).Model(&models.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	adminEmail := strings.ToLower(strings.TrimSpace(cfg.AdminEmail))
	// 未配置 env 管理员凭据：留给「首次注册」流程，而不是让服务启动失败。
	if adminEmail == "" || cfg.AdminPassword == "" {
		return nil
	}
	tenantName := strings.TrimSpace(cfg.DefaultTenant)
	if tenantName == "" {
		return fmt.Errorf("bootstrap default_tenant is required when admin_email is set")
	}
	passwordHash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tenant models.Tenant
		err := tx.Where("name = ?", tenantName).First(&tenant).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("find bootstrap tenant: %w", err)
		}
		if err == gorm.ErrRecordNotFound {
			tenant = models.Tenant{ID: uuid.New(), Name: tenantName, Plan: "enterprise"}
			if err := tx.Create(&tenant).Error; err != nil {
				return fmt.Errorf("create bootstrap tenant: %w", err)
			}
		}
		admin := models.User{
			ID:           uuid.New(),
			TenantID:     tenant.ID,
			Email:        adminEmail,
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
