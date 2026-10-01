package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/models"
)

type UserService struct {
	db *gorm.DB
}

func NewUserService(db *gorm.DB) *UserService {
	return &UserService{db: db}
}

type UserInput struct {
	TenantID *uuid.UUID `json:"tenant_id,omitempty"`
	Email    string     `json:"email"`
	Password string     `json:"password,omitempty"`
	Role     string     `json:"role"`
	Status   string     `json:"status"`
}

func (s *UserService) List(ctx context.Context, tenantID *uuid.UUID, page, pageSize int) ([]models.User, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.User{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	var users []models.User
	err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return users, total, nil
}

func (s *UserService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.User, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var user models.User
	err := query.First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return models.User{}, ErrNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

func (s *UserService) Create(ctx context.Context, input UserInput, defaultTenantID uuid.UUID, allowTenantChoice bool) (models.User, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.Email == "" || input.Password == "" || input.Role == "" {
		return models.User{}, fmt.Errorf("%w: email, password, and role are required", ErrInvalidInput)
	}
	if !validRole(input.Role) {
		return models.User{}, fmt.Errorf("%w: unsupported role", ErrInvalidInput)
	}
	tenantID := defaultTenantID
	if allowTenantChoice && input.TenantID != nil {
		tenantID = *input.TenantID
	}
	if tenantID == uuid.Nil {
		return models.User{}, fmt.Errorf("%w: tenant_id is required", ErrInvalidInput)
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		return models.User{}, err
	}
	status := input.Status
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "disabled" {
		return models.User{}, fmt.Errorf("%w: status must be active or disabled", ErrInvalidInput)
	}
	user := models.User{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Email:        input.Email,
		PasswordHash: hash,
		Role:         input.Role,
		Status:       status,
	}
	if err := s.db.WithContext(ctx).Create(&user).Error; err != nil {
		if isUniqueViolation(err) {
			return models.User{}, ErrConflict
		}
		return models.User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

func (s *UserService) Update(ctx context.Context, id uuid.UUID, input UserInput, tenantID *uuid.UUID) (models.User, error) {
	user, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return models.User{}, err
	}
	if input.Email != "" {
		user.Email = strings.ToLower(strings.TrimSpace(input.Email))
	}
	if input.Password != "" {
		hash, hashErr := auth.HashPassword(input.Password)
		if hashErr != nil {
			return models.User{}, hashErr
		}
		user.PasswordHash = hash
	}
	if input.Role != "" {
		if !validRole(input.Role) {
			return models.User{}, fmt.Errorf("%w: unsupported role", ErrInvalidInput)
		}
		user.Role = input.Role
	}
	if input.Status != "" {
		if input.Status != "active" && input.Status != "disabled" {
			return models.User{}, fmt.Errorf("%w: status must be active or disabled", ErrInvalidInput)
		}
		user.Status = input.Status
	}
	if err := s.db.WithContext(ctx).Save(&user).Error; err != nil {
		if isUniqueViolation(err) {
			return models.User{}, ErrConflict
		}
		return models.User{}, fmt.Errorf("update user: %w", err)
	}
	return user, nil
}

func (s *UserService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	user, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(&models.User{}, "id = ?", user.ID).Error; err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

func (s *UserService) ActiveByEmail(ctx context.Context, email string) (models.User, error) {
	var user models.User
	err := s.db.WithContext(ctx).
		Where("email = ? AND status = ?", strings.ToLower(strings.TrimSpace(email)), "active").
		First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return models.User{}, ErrNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("find user: %w", err)
	}
	return user, nil
}

func validRole(role string) bool {
	return auth.RoleAllowed(role,
		auth.RolePlatformAdmin,
		auth.RoleTenantAdmin,
		auth.RoleOps,
		auth.RoleReadonly,
	)
}
