package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
)

// 管理员密码策略（注册与自助轮换共用）。要求不弱于默认强密码：见 README「首次注册」一节。
const (
	// MinAdminPasswordLength 最小长度；默认 env 强密码本身远长于此。
	MinAdminPasswordLength = 16
	// MaxAdminPasswordLength 上限，避免超过 bcrypt 72 字节限制。
	MaxAdminPasswordLength = 128
	// RequiredCharacterClasses 至少混合「大写/小写/数字/符号」中的三类。
	RequiredCharacterClasses = 3
)

// RegisterInput 是「首次登入 = 注册」的入参。
type RegisterInput struct {
	Email string `json:"email"`
	// Password 明文只在本次请求内存中存在，绝不落库/落日志（落库前先 bcrypt）。
	Password string `json:"password"`
	Tenant   string `json:"tenant,omitempty"`
}

// UserCount 返回用户总数，用于判断系统是否已初始化（是否需要首次注册）。
func (s *UserService) UserCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

// NormalizeEmail 小写化 + 去空格，并用 net/mail 校验格式。
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", fmt.Errorf("%w: email is required", ErrInvalidInput)
	}
	if len(email) > 255 {
		return "", fmt.Errorf("%w: email is too long", ErrInvalidInput)
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", fmt.Errorf("%w: invalid email address", ErrInvalidInput)
	}
	return email, nil
}

// ValidatePasswordStrength 校验新密码强度：长度达标，且至少混合三类字符。
func ValidatePasswordStrength(password string) error {
	if len(password) < MinAdminPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, MinAdminPasswordLength)
	}
	if len(password) > MaxAdminPasswordLength {
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidInput, MaxAdminPasswordLength)
	}
	var lower, upper, digit, symbol bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		case !unicode.IsSpace(r):
			symbol = true
		}
	}
	classes := 0
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			classes++
		}
	}
	if classes < RequiredCharacterClasses && len([]rune(password)) < 32 {
		return fmt.Errorf("%w: password must mix at least %d of uppercase, lowercase, digits, and symbols",
			ErrInvalidInput, RequiredCharacterClasses)
	}
	return nil
}

// RegisterFirstAdmin 在系统还没有任何账号时创建第一个平台管理员。
// 已有账号时返回 ErrConflict（调用方映射为 409 already_initialized）。
func (s *UserService) RegisterFirstAdmin(ctx context.Context, input RegisterInput, defaultTenant string) (models.User, error) {
	email, err := NormalizeEmail(input.Email)
	if err != nil {
		return models.User{}, err
	}
	if err := ValidatePasswordStrength(input.Password); err != nil {
		return models.User{}, err
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		return models.User{}, err
	}
	tenantName := strings.TrimSpace(input.Tenant)
	if tenantName == "" {
		tenantName = strings.TrimSpace(defaultTenant)
	}
	if tenantName == "" {
		tenantName = "default"
	}

	var admin models.User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 事务内再次确认「尚无账号」，把注册做成幂等且互斥的一次性动作。
		var count int64
		if err := tx.Model(&models.User{}).Count(&count).Error; err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if count > 0 {
			return ErrConflict
		}
		var tenant models.Tenant
		findErr := tx.Where("name = ?", tenantName).First(&tenant).Error
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("find registration tenant: %w", findErr)
		}
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			tenant = models.Tenant{ID: uuid.New(), Name: tenantName, Plan: "enterprise"}
			if err := tx.Create(&tenant).Error; err != nil {
				if isUniqueViolation(err) {
					return ErrConflict
				}
				return fmt.Errorf("create registration tenant: %w", err)
			}
		}
		admin = models.User{
			ID:           uuid.New(),
			TenantID:     tenant.ID,
			Email:        email,
			PasswordHash: hash,
			Role:         auth.RolePlatformAdmin,
			Status:       "active",
		}
		if err := tx.Create(&admin).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return fmt.Errorf("create registration admin: %w", err)
		}
		return nil
	})
	if err != nil {
		return models.User{}, err
	}
	return admin, nil
}

// ChangeEmail 修改登录邮箱；必须校验当前密码，新邮箱不得被占用（唯一索引兜底）。
func (s *UserService) ChangeEmail(ctx context.Context, id uuid.UUID, currentPassword, newEmail string) (models.User, error) {
	user, err := s.Get(ctx, id, nil)
	if err != nil {
		return models.User{}, err
	}
	if !auth.CheckPassword(user.PasswordHash, currentPassword) {
		return models.User{}, fmt.Errorf("%w: current password is incorrect", ErrForbidden)
	}
	email, err := NormalizeEmail(newEmail)
	if err != nil {
		return models.User{}, err
	}
	if email == user.Email {
		return user, nil
	}
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", user.ID).Update("email", email).Error; err != nil {
		if isUniqueViolation(err) {
			return models.User{}, fmt.Errorf("%w: email is already in use", ErrConflict)
		}
		return models.User{}, fmt.Errorf("update user email: %w", err)
	}
	user.Email = email
	return user, nil
}

// RotatePassword 轮换登录密码：校验当前密码 + 新密码强度，改密后 token_version +1
// 让旧 refresh token 失效。返回刷新后的用户（带新的 token_version）。
func (s *UserService) RotatePassword(ctx context.Context, id uuid.UUID, currentPassword, newPassword string) (models.User, error) {
	user, err := s.Get(ctx, id, nil)
	if err != nil {
		return models.User{}, err
	}
	if !auth.CheckPassword(user.PasswordHash, currentPassword) {
		return models.User{}, fmt.Errorf("%w: current password is incorrect", ErrForbidden)
	}
	if err := ValidatePasswordStrength(newPassword); err != nil {
		return models.User{}, err
	}
	if auth.CheckPassword(user.PasswordHash, newPassword) {
		return models.User{}, fmt.Errorf("%w: new password must differ from the current password", ErrInvalidInput)
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return models.User{}, err
	}
	if err := s.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", user.ID).
		Updates(map[string]any{
			"password_hash": hash,
			"token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
		return models.User{}, fmt.Errorf("rotate password: %w", err)
	}
	// 回读以拿到自增后的真实 token_version。
	updated, err := s.Get(ctx, id, nil)
	if err != nil {
		return models.User{}, err
	}
	return updated, nil
}
