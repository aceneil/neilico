package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type APIToken struct {
	ID          uuid.UUID                   `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID    uuid.UUID                   `gorm:"type:uuid;not null;index;uniqueIndex:idx_api_tokens_tenant_name,priority:1" json:"tenant_id"`
	UserID      *uuid.UUID                  `gorm:"type:uuid;index" json:"user_id,omitempty"`
	Name        string                      `gorm:"type:varchar(255);not null;uniqueIndex:idx_api_tokens_tenant_name,priority:2" json:"name"`
	TokenHash   string                      `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	TokenPrefix string                      `gorm:"type:varchar(8);not null" json:"token_prefix"`
	Scopes      datatypes.JSONSlice[string] `gorm:"type:jsonb;not null" json:"scopes"`
	ExpiresAt   *time.Time                  `gorm:"type:timestamp" json:"expires_at,omitempty"`
	LastUsedAt  *time.Time                  `gorm:"type:timestamp" json:"last_used_at,omitempty"`
	LastUsedIP  string                      `gorm:"type:varchar(64);not null;default:''" json:"last_used_ip,omitempty"`
	RevokedAt   *time.Time                  `gorm:"type:timestamp;index" json:"revoked_at,omitempty"`
	CreatedAt   time.Time                   `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"-"`
	User   *User   `gorm:"foreignKey:UserID;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
}
