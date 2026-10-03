package models

import (
	"time"

	"github.com/google/uuid"
)

type NodeEnrollToken struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"tenant_id"`
	NetworkID *uuid.UUID `gorm:"type:uuid;index" json:"network_id,omitempty"`
	NameHint  string     `gorm:"type:varchar(255);not null;default:''" json:"name_hint"`
	TokenHash string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	ExpiresAt time.Time  `gorm:"type:timestamp;not null;index" json:"expires_at"`
	MaxUses   int        `gorm:"not null;default:1" json:"max_uses"`
	UsedCount int        `gorm:"not null;default:0" json:"used_count"`
	RevokedAt *time.Time `gorm:"type:timestamp;index" json:"revoked_at,omitempty"`
	CreatedBy *uuid.UUID `gorm:"type:uuid;index" json:"created_by,omitempty"`
	CreatedAt time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP;index" json:"created_at"`

	Tenant  *Tenant         `gorm:"foreignKey:TenantID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"-"`
	Network *VirtualNetwork `gorm:"foreignKey:NetworkID;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
	User    *User           `gorm:"foreignKey:CreatedBy;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
}

type NodeEnrollment struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TokenID     uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_node_enrollments_token_request,priority:1" json:"token_id"`
	NodeID      uuid.UUID `gorm:"type:uuid;not null;uniqueIndex" json:"node_id"`
	RequestHash string    `gorm:"type:char(64);not null;uniqueIndex:idx_node_enrollments_token_request,priority:2" json:"-"`
	CreatedAt   time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP;index" json:"created_at"`

	Token *NodeEnrollToken `gorm:"foreignKey:TokenID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"-"`
	Node  *Node            `gorm:"foreignKey:NodeID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"-"`
}
