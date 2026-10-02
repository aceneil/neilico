package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
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
	PrivateKey     string                      `gorm:"type:text;not null;default:''" json:"-"`
	VirtualIP      *string                     `gorm:"type:inet" json:"virtual_ip"`
	PublicEndpoint *string                     `gorm:"type:varchar(255)" json:"public_endpoint,omitempty"`
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

type CA struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Name            string    `gorm:"type:varchar(255);not null;index" json:"name"`
	CertPEM         string    `gorm:"type:text;not null" json:"cert_pem"`
	EncryptedKeyPEM string    `gorm:"type:text;not null" json:"-"`
	NotBefore       time.Time `gorm:"type:timestamp;not null" json:"not_before"`
	NotAfter        time.Time `gorm:"type:timestamp;not null;index" json:"not_after"`
	CreatedAt       time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`
}

type NodeCertificate struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	NodeID          uuid.UUID `gorm:"type:uuid;not null;index" json:"node_id"`
	CAID            uuid.UUID `gorm:"type:uuid;not null;index" json:"ca_id"`
	SerialNumber    string    `gorm:"type:varchar(128);not null;index" json:"serial_number"`
	Fingerprint     string    `gorm:"type:varchar(128);not null" json:"fingerprint"`
	CertPEM         string    `gorm:"type:text;not null" json:"-"`
	EncryptedKeyPEM string    `gorm:"type:text;not null" json:"-"`
	NotBefore       time.Time `gorm:"type:timestamp;not null" json:"not_before"`
	NotAfter        time.Time `gorm:"type:timestamp;not null;index" json:"not_after"`
	CreatedAt       time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`
}

type Certificate struct {
	ID              uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID        uuid.UUID  `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Domain          string     `gorm:"type:varchar(255);not null;index" json:"domain"`
	Issuer          string     `gorm:"type:varchar(255);not null" json:"issuer"`
	CertPEM         string     `gorm:"type:text;not null;default:''" json:"cert_pem"`
	KeyPEM          string     `gorm:"type:text;not null;default:''" json:"-"`
	ExpiresAt       *time.Time `gorm:"type:timestamp" json:"expires_at"`
	Status          string     `gorm:"type:varchar(32);not null;default:active;index" json:"status"`
	LastError       string     `gorm:"type:text;not null;default:''" json:"last_error"`
	RenewedAt       *time.Time `gorm:"type:timestamp" json:"renewed_at"`
	RenewCount      int        `gorm:"not null;default:0" json:"renew_count"`
	ChallengeType   string     `gorm:"type:varchar(32);not null;default:''" json:"challenge_type"`
	AutoRenew       bool       `gorm:"not null;default:true" json:"auto_renew"`
	NextAttemptAt   *time.Time `gorm:"type:timestamp" json:"next_attempt_at,omitempty"`
	RenewalFailures int        `gorm:"not null;default:0" json:"-"`
	CreatedAt       time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type Domain struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Domain    string     `gorm:"type:varchar(255);not null;uniqueIndex" json:"domain"`
	CertID    *uuid.UUID `gorm:"type:uuid;index" json:"cert_id"`
	Status    string     `gorm:"type:varchar(32);not null;default:pending" json:"status"`
	CreatedAt time.Time  `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant      *Tenant      `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Certificate *Certificate `gorm:"foreignKey:CertID;constraint:OnDelete:SET NULL,OnUpdate:CASCADE" json:"-"`
}

type ProxyRule struct {
	ID                         uuid.UUID     `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID                   uuid.UUID     `gorm:"type:uuid;not null;index" json:"tenant_id"`
	DomainID                   uuid.UUID     `gorm:"type:uuid;not null;index" json:"domain_id"`
	Path                       string        `gorm:"type:varchar(255);not null;default:/" json:"path"`
	TargetType                 string        `gorm:"type:varchar(32);not null" json:"target_type"`
	Target                     string        `gorm:"type:varchar(255);not null" json:"target"`
	UpstreamScheme             string        `gorm:"type:varchar(16);not null;default:http" json:"upstream_scheme"`
	UpstreamInsecureSkipVerify bool          `gorm:"not null;default:false" json:"upstream_insecure_skip_verify"`
	UpstreamCAFile             string        `gorm:"type:varchar(512);not null;default:''" json:"upstream_ca_file,omitempty"`
	AccessControl              AccessControl `gorm:"type:jsonb;not null" json:"access_control"`
	Enabled                    bool          `gorm:"not null;default:true" json:"enabled"`
	CreatedAt                  time.Time     `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Domain *Domain `gorm:"foreignKey:DomainID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type BasicAuth struct {
	Enabled      bool   `json:"enabled"`
	Username     string `json:"username,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
}

func (b *BasicAuth) UnmarshalJSON(data []byte) error {
	if string(data) == "false" || string(data) == "null" {
		*b = BasicAuth{}
		return nil
	}
	type basicAuth BasicAuth
	var decoded basicAuth
	if err := json.Unmarshal(data, &decoded); err != nil {
		return errors.New("basic_auth must be false or an object")
	}
	*b = BasicAuth(decoded)
	return nil
}

type AccessControl struct {
	IPWhitelist []string  `json:"ip_whitelist"`
	BasicAuth   BasicAuth `json:"basic_auth"`
	RequireJWT  bool      `json:"require_jwt"`
}

func (a AccessControl) Value() (driver.Value, error) {
	return json.Marshal(a)
}

func (a *AccessControl) Scan(value any) error {
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = append(raw[:0], typed...)
	case string:
		raw = []byte(typed)
	default:
		return fmt.Errorf("cannot scan %T into AccessControl", value)
	}
	if len(raw) == 0 {
		*a = AccessControl{IPWhitelist: []string{}}
		return nil
	}
	return json.Unmarshal(raw, a)
}

type VirtualNetwork struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID     uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_virtual_networks_tenant_name,priority:1" json:"tenant_id"`
	Name         string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_virtual_networks_tenant_name,priority:2" json:"name"`
	CIDR         string    `gorm:"column:cidr;type:cidr;not null" json:"cidr"`
	Secret       string    `gorm:"type:text;not null;default:''" json:"-"`
	PresharedKey string    `gorm:"type:text;not null;default:''" json:"-"`
	CreatedAt    time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type NetworkMember struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	NetworkID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_network_members_network_node,priority:1;uniqueIndex:idx_network_members_network_ip,priority:1" json:"network_id"`
	NodeID    uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_network_members_network_node,priority:2" json:"node_id"`
	VirtualIP string    `gorm:"type:inet;not null;uniqueIndex:idx_network_members_network_ip,priority:2" json:"virtual_ip"`
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
	CIDR      string    `gorm:"column:cidr;type:cidr;not null" json:"cidr"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`

	Network *VirtualNetwork `gorm:"foreignKey:NetworkID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Node    *Node           `gorm:"foreignKey:NodeID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}

type RelayServer struct {
	ID       uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Name     string     `gorm:"type:varchar(255);not null;uniqueIndex" json:"name"`
	Endpoint string     `gorm:"type:varchar(255);not null" json:"endpoint"`
	Region   string     `gorm:"type:varchar(64);not null" json:"region"`
	Status   string     `gorm:"type:varchar(32);not null;default:offline" json:"status"`
	LastSeen *time.Time `gorm:"type:timestamp;index" json:"last_seen"`
}

type ConfigVersion struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID   uuid.UUID      `gorm:"type:uuid;not null;index" json:"tenant_id"`
	TargetType string         `gorm:"type:varchar(32);not null;uniqueIndex:idx_config_versions_target_version,priority:1" json:"target_type"`
	TargetID   uuid.UUID      `gorm:"type:uuid;not null;index;uniqueIndex:idx_config_versions_target_version,priority:2" json:"target_id"`
	Version    int            `gorm:"not null;uniqueIndex:idx_config_versions_target_version,priority:3" json:"version"`
	Config     datatypes.JSON `gorm:"type:jsonb;not null" json:"config"`
	Reason     string         `gorm:"type:varchar(255);not null;default:''" json:"reason,omitempty"`
	Summary    datatypes.JSON `gorm:"type:jsonb;not null" json:"summary,omitempty"`
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

type ConfigDispatchFailure struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID   uuid.UUID `gorm:"type:uuid;not null;index" json:"tenant_id"`
	TargetType string    `gorm:"type:varchar(32);not null;uniqueIndex:idx_config_dispatch_target,priority:1" json:"target_type"`
	TargetID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_config_dispatch_target,priority:2" json:"target_id"`
	LastError  string    `gorm:"type:text;not null" json:"last_error"`
	Failures   int       `gorm:"not null;default:1" json:"failures"`
	FailedAt   time.Time `gorm:"type:timestamp;not null;index" json:"failed_at"`
	CreatedAt  time.Time `gorm:"type:timestamp;not null" json:"created_at"`
	UpdatedAt  time.Time `gorm:"type:timestamp;not null" json:"updated_at"`
}

type Alert struct {
	ID              uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID        uuid.UUID  `gorm:"type:uuid;not null;index;uniqueIndex:idx_alerts_tenant_rule_target,priority:1" json:"tenant_id"`
	Rule            string     `gorm:"type:varchar(64);not null;index:idx_alerts_rule_target,priority:1;uniqueIndex:idx_alerts_tenant_rule_target,priority:2" json:"rule"`
	Severity        string     `gorm:"type:varchar(16);not null;index" json:"severity"`
	TargetType      string     `gorm:"type:varchar(32);not null;index:idx_alerts_rule_target,priority:2;uniqueIndex:idx_alerts_tenant_rule_target,priority:3" json:"target_type"`
	TargetID        uuid.UUID  `gorm:"type:uuid;not null;index:idx_alerts_rule_target,priority:3;uniqueIndex:idx_alerts_tenant_rule_target,priority:4" json:"target_id"`
	Title           string     `gorm:"type:varchar(255);not null" json:"title"`
	Detail          string     `gorm:"type:text;not null" json:"detail"`
	Value           float64    `gorm:"not null" json:"value"`
	Threshold       float64    `gorm:"not null" json:"threshold"`
	Since           time.Time  `gorm:"type:timestamp;not null;index" json:"since"`
	StartedAt       time.Time  `gorm:"type:timestamp;not null" json:"started_at"`
	State           string     `gorm:"type:varchar(16);not null;index" json:"state"`
	EvaluationCount int        `gorm:"not null;default:0" json:"-"`
	LastEvaluatedAt time.Time  `gorm:"type:timestamp;not null;index" json:"last_evaluated_at"`
	ResolvedAt      *time.Time `gorm:"type:timestamp;index" json:"resolved_at,omitempty"`
	CreatedAt       time.Time  `gorm:"type:timestamp;not null;index" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"type:timestamp;not null" json:"updated_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"-"`
}

type AlertEvent struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	AlertID    uuid.UUID `gorm:"type:uuid;not null;index;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"alert_id"`
	TenantID   uuid.UUID `gorm:"type:uuid;not null;index" json:"tenant_id"`
	Rule       string    `gorm:"type:varchar(64);not null;index" json:"rule"`
	TargetType string    `gorm:"type:varchar(32);not null" json:"target_type"`
	TargetID   uuid.UUID `gorm:"type:uuid;not null;index" json:"target_id"`
	State      string    `gorm:"type:varchar(16);not null;index" json:"state"`
	Severity   string    `gorm:"type:varchar(16);not null" json:"severity"`
	Value      float64   `gorm:"not null" json:"value"`
	Threshold  float64   `gorm:"not null" json:"threshold"`
	Detail     string    `gorm:"type:text;not null" json:"detail"`
	CreatedAt  time.Time `gorm:"type:timestamp;not null;index" json:"created_at"`

	Alert *Alert `gorm:"foreignKey:AlertID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE" json:"-"`
}

type TrafficLog struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TenantID  uuid.UUID `gorm:"type:uuid;not null;index" json:"tenant_id"`
	NodeID    uuid.UUID `gorm:"type:uuid;not null;index:idx_traffic_logs_node_created,priority:1" json:"node_id"`
	Direction string    `gorm:"type:varchar(8);not null" json:"direction"`
	Bytes     int64     `gorm:"type:bigint;not null" json:"bytes"`
	Protocol  string    `gorm:"type:varchar(32);not null" json:"protocol"`
	Peer      string    `gorm:"type:varchar(255);not null" json:"peer"`
	CreatedAt time.Time `gorm:"type:timestamp;not null;index:idx_traffic_logs_node_created,priority:2" json:"created_at"`

	Tenant *Tenant `gorm:"foreignKey:TenantID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
	Node   *Node   `gorm:"foreignKey:NodeID;constraint:OnDelete:RESTRICT,OnUpdate:CASCADE" json:"-"`
}
