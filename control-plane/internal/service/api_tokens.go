package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
)

var ErrTokenRevoked = errors.New("API token revoked")

type APITokenInput struct {
	Name          string
	Scopes        []string
	ExpiresInDays *int
	UserID        *uuid.UUID
}

type APITokenCreated struct {
	Token    string
	APIToken models.APIToken
}

type APITokenService struct {
	db *gorm.DB
}

func NewAPITokenService(db *gorm.DB) *APITokenService {
	return &APITokenService{db: db}
}

func (s *APITokenService) Create(ctx context.Context, tenantID uuid.UUID, input APITokenInput) (APITokenCreated, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return APITokenCreated{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if len(name) > 255 {
		return APITokenCreated{}, fmt.Errorf("%w: name must be at most 255 characters", ErrInvalidInput)
	}
	scopes, err := auth.NormalizeScopes(input.Scopes)
	if err != nil || len(scopes) == 0 {
		return APITokenCreated{}, fmt.Errorf("%w: at least one valid scope is required", ErrInvalidInput)
	}
	var expiresAt *time.Time
	if input.ExpiresInDays != nil {
		if *input.ExpiresInDays < 1 {
			return APITokenCreated{}, fmt.Errorf("%w: expires_in_days must be positive", ErrInvalidInput)
		}
		value := time.Now().UTC().AddDate(0, 0, *input.ExpiresInDays)
		expiresAt = &value
	}
	plain, hash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		return APITokenCreated{}, err
	}
	item := models.APIToken{
		ID:          uuid.New(),
		TenantID:    tenantID,
		UserID:      input.UserID,
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return APITokenCreated{}, mapUniqueError(err, "API token name already exists")
	}
	return APITokenCreated{Token: plain, APIToken: item}, nil
}

func (s *APITokenService) List(ctx context.Context, tenantID *uuid.UUID) ([]models.APIToken, error) {
	query := s.db.WithContext(ctx).Model(&models.APIToken{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	items := make([]models.APIToken, 0)
	if err := query.Order("created_at DESC, id DESC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	return items, nil
}

func (s *APITokenService) Get(ctx context.Context, tenantID *uuid.UUID, id uuid.UUID) (models.APIToken, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.APIToken
	if err := query.First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.APIToken{}, ErrNotFound
		}
		return models.APIToken{}, fmt.Errorf("get API token: %w", err)
	}
	return item, nil
}

func (s *APITokenService) Revoke(ctx context.Context, tenantID *uuid.UUID, id uuid.UUID) (models.APIToken, bool, error) {
	item, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return models.APIToken{}, false, err
	}
	if item.RevokedAt != nil {
		return item, true, nil
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&models.APIToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now)
	if result.Error != nil {
		return models.APIToken{}, false, fmt.Errorf("revoke API token: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		item, err = s.Get(ctx, tenantID, id)
		return item, err == nil, err
	}
	item.RevokedAt = &now
	return item, false, nil
}

func (s *APITokenService) Rotate(ctx context.Context, tenantID *uuid.UUID, id uuid.UUID) (APITokenCreated, error) {
	var created APITokenCreated
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("id = ?", id)
		if tenantID != nil {
			query = query.Where("tenant_id = ?", *tenantID)
		}
		var old models.APIToken
		if err := query.First(&old).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return fmt.Errorf("get API token for rotation: %w", err)
		}
		if old.RevokedAt != nil {
			return ErrTokenRevoked
		}
		now := time.Now().UTC()
		if err := tx.Model(&models.APIToken{}).Where("id = ?", old.ID).Update("revoked_at", now).Error; err != nil {
			return fmt.Errorf("revoke rotated API token: %w", err)
		}
		oldName := old.Name
		rotatedName := fmt.Sprintf("%s (rotated %s)", old.Name, old.ID.String()[:8])
		if len(rotatedName) > 255 {
			rotatedName = rotatedName[:255]
		}
		if err := tx.Model(&models.APIToken{}).Where("id = ?", old.ID).Update("name", rotatedName).Error; err != nil {
			return fmt.Errorf("rename rotated API token: %w", err)
		}
		plain, hash, prefix, err := auth.GenerateAPIToken()
		if err != nil {
			return err
		}
		item := models.APIToken{
			ID:          uuid.New(),
			TenantID:    old.TenantID,
			UserID:      old.UserID,
			Name:        oldName,
			TokenHash:   hash,
			TokenPrefix: prefix,
			Scopes:      append([]string(nil), old.Scopes...),
			ExpiresAt:   old.ExpiresAt,
			CreatedAt:   now,
		}
		if err := tx.Create(&item).Error; err != nil {
			return mapUniqueError(err, "API token name already exists")
		}
		created = APITokenCreated{Token: plain, APIToken: item}
		return nil
	})
	if err != nil {
		return APITokenCreated{}, err
	}
	return created, nil
}

func mapUniqueError(err error, message string) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "duplicate") || strings.Contains(text, "unique") || strings.Contains(text, "constraint") {
		return fmt.Errorf("%w: %s", ErrConflict, message)
	}
	return fmt.Errorf("create API token: %w", err)
}
