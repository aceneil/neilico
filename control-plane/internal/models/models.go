package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type Tenant struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name      string    `gorm:"type:varchar(255);not null;uniqueIndex" json:"name"`
	Plan      string    `gorm:"type:varchar(64);not null;default:free" json:"plan"`
	CreatedAt time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`
}

type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID     uuid.UUID `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Email        string    `gorm:"type:varchar(255);not null;uniqueIndex" json:"email"`
	PasswordHash string    `gorm:"type:varchar(255);not null" json:"-"`
	Role         string    `gorm:"type:varchar(32);not null" json:"role"`
	Status       string    `gorm:"type:varchar(32);not null;default:active" json:"status"`
	CreatedAt    time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type Node struct {
	ID             uuid.UUID                   `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID       uuid.UUID                   `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Name           string                      `gorm:"type:varchar(255);not null" json:"name"`
	PublicKey      string                      `gorm:"type:text;not null" json:"public_key"`
	VirtualIP      *string                     `gorm:"type:inet" json:"virtual_ip"`
	OS             string                      `gorm:"type:varchar(64);not null" json:"os"`
	Arch           string                      `gorm:"type:varchar(64);not null" json:"arch"`
	Version        string                      `gorm:"type:varchar(64);not null" json:"version"`
	Status         string                      `gorm:"type:varchar(32);not null;default:offline;index" json:"status"`
	LastSeen       *time.Time                  `gorm:"type:timestamp" json:"last_seen"`
	Tags           datatypes.JSONSlice[string] `gorm:"type:jsonb;not null" json:"tags"`
	AgentTokenHash string                      `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	CreatedAt      time.Time                   `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type Certificate struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Domain    string     `gorm:"type:varchar(255);not null;index" json:"domain"`
	Issuer    string     `gorm:"type:varchar(255);not null" json:"issuer"`
	CertPEM   string     `gorm:"type:text;not null" json:"cert_pem"`
	KeyPEM    string     `gorm:"type:text;not null" json:"-"`
	ExpiresAt *time.Time `gorm:"type:timestamp" json:"expires_at"`
}

type Domain struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Domain    string     `gorm:"type:varchar(255);not null" json:"domain"`
	CertID    *uuid.UUID `gorm:"type:uuid;index" json:"cert_id"`
	Status    string     `gorm:"type:varchar(32);not null;default:pending" json:"status"`
	CreatedAt time.Time  `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant      *Tenant      `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Certificate *Certificate `gorm:"foreignKey:CertID;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
}

type ProxyRule struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID      uuid.UUID      `gorm:"type:uuid;not null;index" json:"tenant_id"`
	DomainID      uuid.UUID      `gorm:"type:uuid;not null;index" json:"domain_id"`
	Path          string         `gorm:"type:varchar(255);not null;default:/" json:"path"`
	TargetType    string         `gorm:"type:varchar(32);not null" json:"target_type"`
	Target        string         `gorm:"type:varchar(255);not null" json:"target"`
	AccessControl datatypes.JSON `gorm:"type:jsonb;not null" json:"access_control"`
	Enabled       bool           `gorm:"not null;default:true" json:"enabled"`
	CreatedAt     time.Time      `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Domain *Domain `gorm:"foreignKey:DomainID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type VirtualNetwork struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  uuid.UUID `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Name      string    `gorm:"type:varchar(255);not null" json:"name"`
	CIDR      string    `gorm:"type:cidr;not null" json:"cidr"`
	CreatedAt time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type NetworkMember struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	NetworkID uuid.UUID `gorm:"type:uuid;not null;index" json:"network_id"`
	NodeID    uuid.UUID `gorm:"type:uuid;not null;index" json:"node_id"`
	VirtualIP string    `gorm:"type:inet;not null" json:"virtual_ip"`
	Role      string    `gorm:"type:varchar(32);not null;default:member" json:"role"`
	JoinedAt  time.Time `gorm:"type:timestamp;not null;index" json:"joined_at"`

	Network *VirtualNetwork `gorm:"foreignKey:NetworkID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Node    *Node           `gorm:"foreignKey:NodeID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type ACLRule struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	NetworkID uuid.UUID `gorm:"type:uuid;not null;index" json:"network_id"`
	Src       string    `gorm:"type:varchar(255);not null" json:"src"`
	Dst       string    `gorm:"type:varchar(255);not null" json:"dst"`
	Action    string    `gorm:"type:varchar(16);not null" json:"action"`
	Protocol  string    `gorm:"type:varchar(16);not null;default:any" json:"protocol"`
	Ports     string    `gorm:"type:varchar(255);not null;default:any" json:"ports"`
	Priority  int       `gorm:"not null;index" json:"priority"`

	Network *VirtualNetwork `gorm:"foreignKey:NetworkID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type SubnetRoute struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	NetworkID uuid.UUID `gorm:"type:uuid;not null;index" json:"network_id"`
	NodeID    uuid.UUID `gorm:"type:uuid;not null;index" json:"node_id"`
	CIDR      string    `gorm:"type:cidr;not null" json:"cidr"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`

	Network *VirtualNetwork `gorm:"foreignKey:NetworkID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Node    *Node           `gorm:"foreignKey:NodeID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type RelayServer struct {
	ID       uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Name     string     `gorm:"type:varchar(255);not null" json:"name"`
	Endpoint string     `gorm:"type:varchar(255);not null" json:"endpoint"`
	Region   string     `gorm:"type:varchar(64);not null" json:"region"`
	Status   string     `gorm:"type:varchar(32);not null;default:unknown" json:"status"`
	LastSeen *time.Time `gorm:"type:timestamp;index" json:"last_seen"`
}

type ConfigVersion struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID   uuid.UUID      `gorm:"type:uuid;not null;index" json:"tenant_id"`
	TargetType string         `gorm:"type:varchar(32);not null" json:"target_type"`
	TargetID   uuid.UUID      `gorm:"type:uuid;not null;index" json:"target_id"`
	Version    int            `gorm:"not null" json:"version"`
	Config     datatypes.JSON `gorm:"type:jsonb;not null" json:"config"`
	CreatedAt  time.Time      `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type AuditLog struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  *uuid.UUID     `gorm:"type:uuid;index" json:"tenant_id,omitempty"`
	UserID    *uuid.UUID     `gorm:"type:uuid;index" json:"user_id,omitempty"`
	Action    string         `gorm:"type:varchar(255);not null" json:"action"`
	Resource  string         `gorm:"type:varchar(255);not null" json:"resource"`
	Detail    datatypes.JSON `gorm:"type:jsonb;not null" json:"detail"`
	IP        string         `gorm:"type:inet;not null;default:0.0.0.0" json:"ip"`
	CreatedAt time.Time      `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
	User   *User   `gorm:"foreignKey:UserID;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
}

type TrafficLog struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  uuid.UUID `gorm:"type:uuid;not null;index" json:"tenant_id"`
	NodeID    uuid.UUID `gorm:"type:uuid;not null;index" json:"node_id"`
	Direction string    `gorm:"type:varchar(8);not null" json:"direction"`
	Bytes     int64     `gorm:"type:bigint;not null" json:"bytes"`
	Protocol  string    `gorm:"type:varchar(32);not null" json:"protocol"`
	Peer      string    `gorm:"type:varchar(255);not null" json:"peer"`
	CreatedAt time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Node   *Node   `gorm:"foreignKey:NodeID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}
