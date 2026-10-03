package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
	"neilico/control-plane/pkg/enrolltoken"
)

var (
	ErrEnrollUnauthorized = errors.New("unauthorized enrollment token")
	ErrEnrollGone         = errors.New("enrollment token exhausted")
)

type NodeEnrollTokenInput struct {
	NetworkID        *uuid.UUID `json:"network_id,omitempty"`
	NameHint         string     `json:"name_hint,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	ExpiresIn        *int       `json:"expires_in,omitempty"`
	ExpiresInSeconds *int       `json:"expires_in_seconds,omitempty"`
	ExpiresInDays    *int       `json:"expires_in_days,omitempty"`
	MaxUses          int        `json:"max_uses,omitempty"`
}

type NodeEnrollTokenCreated struct {
	Token string
	Item  models.NodeEnrollToken
}

type NodeEnrollTokenService struct {
	db         *gorm.DB
	signingKey []byte
}

func NewNodeEnrollTokenService(db *gorm.DB, signingKey []byte) *NodeEnrollTokenService {
	return &NodeEnrollTokenService{db: db, signingKey: append([]byte(nil), signingKey...)}
}

func (s *NodeEnrollTokenService) Create(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, input NodeEnrollTokenInput, server string) (NodeEnrollTokenCreated, error) {
	if len(s.signingKey) == 0 {
		return NodeEnrollTokenCreated{}, errors.New("enrollment signing key is unavailable")
	}
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return NodeEnrollTokenCreated{}, fmt.Errorf("%w: enrollment server URL is required", ErrInvalidInput)
	}
	nameHint := strings.TrimSpace(input.NameHint)
	if len(nameHint) > 255 {
		return NodeEnrollTokenCreated{}, fmt.Errorf("%w: name_hint must be at most 255 characters", ErrInvalidInput)
	}
	maxUses := input.MaxUses
	if maxUses == 0 {
		maxUses = 1
	}
	if maxUses < 1 || maxUses > 1000 {
		return NodeEnrollTokenCreated{}, fmt.Errorf("%w: max_uses must be between 1 and 1000", ErrInvalidInput)
	}
	if input.NetworkID != nil {
		var network models.VirtualNetwork
		if err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", *input.NetworkID, tenantID).First(&network).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return NodeEnrollTokenCreated{}, ErrNotFound
			}
			return NodeEnrollTokenCreated{}, fmt.Errorf("get enrollment network: %w", err)
		}
	}
	expiresAt, err := enrollExpiry(input)
	if err != nil {
		return NodeEnrollTokenCreated{}, err
	}
	item := models.NodeEnrollToken{
		ID:        uuid.New(),
		TenantID:  tenantID,
		NetworkID: input.NetworkID,
		NameHint:  nameHint,
		ExpiresAt: expiresAt,
		MaxUses:   maxUses,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	payload := enrolltoken.Payload{
		Version:   1,
		Server:    server,
		TenantID:  tenantID,
		NetworkID: input.NetworkID,
		TokenID:   item.ID,
		ExpiresAt: expiresAt.Unix(),
	}
	plain, err := enrolltoken.Sign(payload, s.signingKey)
	if err != nil {
		return NodeEnrollTokenCreated{}, err
	}
	item.TokenHash = enrolltoken.Hash(plain)
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return NodeEnrollTokenCreated{}, fmt.Errorf("create enrollment token: %w", err)
	}
	return NodeEnrollTokenCreated{Token: plain, Item: item}, nil
}

func enrollExpiry(input NodeEnrollTokenInput) (time.Time, error) {
	set := 0
	if input.ExpiresAt != nil {
		set++
	}
	if input.ExpiresIn != nil {
		set++
	}
	if input.ExpiresInSeconds != nil {
		set++
	}
	if input.ExpiresInDays != nil {
		set++
	}
	if set > 1 {
		return time.Time{}, fmt.Errorf("%w: specify only one expiry option", ErrInvalidInput)
	}
	now := time.Now().UTC()
	switch {
	case input.ExpiresAt != nil:
		value := input.ExpiresAt.UTC()
		if !value.After(now) {
			return time.Time{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidInput)
		}
		return value.Truncate(time.Second), nil
	case input.ExpiresIn != nil:
		if *input.ExpiresIn < 60 {
			return time.Time{}, fmt.Errorf("%w: expires_in must be at least 60 seconds", ErrInvalidInput)
		}
		return now.Add(time.Duration(*input.ExpiresIn) * time.Second).Truncate(time.Second), nil
	case input.ExpiresInSeconds != nil:
		if *input.ExpiresInSeconds < 60 {
			return time.Time{}, fmt.Errorf("%w: expires_in_seconds must be at least 60 seconds", ErrInvalidInput)
		}
		return now.Add(time.Duration(*input.ExpiresInSeconds) * time.Second).Truncate(time.Second), nil
	case input.ExpiresInDays != nil:
		if *input.ExpiresInDays < 1 {
			return time.Time{}, fmt.Errorf("%w: expires_in_days must be positive", ErrInvalidInput)
		}
		return now.AddDate(0, 0, *input.ExpiresInDays).Truncate(time.Second), nil
	default:
		return now.Add(24 * time.Hour).Truncate(time.Second), nil
	}
}

func (s *NodeEnrollTokenService) List(ctx context.Context, tenantID *uuid.UUID) ([]models.NodeEnrollToken, error) {
	query := s.db.WithContext(ctx).Model(&models.NodeEnrollToken{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	items := make([]models.NodeEnrollToken, 0)
	if err := query.Order("created_at DESC, id DESC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list enrollment tokens: %w", err)
	}
	return items, nil
}

func (s *NodeEnrollTokenService) Revoke(ctx context.Context, tenantID *uuid.UUID, id uuid.UUID) (models.NodeEnrollToken, bool, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.NodeEnrollToken
	if err := query.First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.NodeEnrollToken{}, false, ErrNotFound
		}
		return models.NodeEnrollToken{}, false, fmt.Errorf("get enrollment token: %w", err)
	}
	if item.RevokedAt != nil {
		return item, true, nil
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&models.NodeEnrollToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now).Error; err != nil {
		return models.NodeEnrollToken{}, false, fmt.Errorf("revoke enrollment token: %w", err)
	}
	item.RevokedAt = &now
	return item, false, nil
}

func NodeEnrollTokenStatus(item models.NodeEnrollToken, now time.Time) string {
	switch {
	case item.RevokedAt != nil:
		return "revoked"
	case !item.ExpiresAt.After(now):
		return "expired"
	case item.UsedCount >= item.MaxUses:
		return "used"
	default:
		return "active"
	}
}

type NodeEnrollInput struct {
	Token     string   `json:"token"`
	Name      string   `json:"name,omitempty"`
	Hostname  string   `json:"hostname,omitempty"`
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	Version   string   `json:"version"`
	Tags      []string `json:"tags,omitempty"`
	PublicKey string   `json:"public_key,omitempty"`
}

type NodeEnrollOutput struct {
	NodeID     uuid.UUID  `json:"node_id"`
	AgentToken string     `json:"agent_token"`
	PrivateKey string     `json:"private_key"`
	PublicKey  string     `json:"public_key,omitempty"`
	VirtualIP  string     `json:"virtual_ip,omitempty"`
	Server     string     `json:"server"`
	NetworkID  *uuid.UUID `json:"network_id,omitempty"`
	Replayed   bool       `json:"replayed,omitempty"`
}

type NodeEnrollService struct {
	db         *gorm.DB
	nodes      *NodeService
	signingKey []byte
}

func NewNodeEnrollService(db *gorm.DB, nodes *NodeService, signingKey []byte) *NodeEnrollService {
	return &NodeEnrollService{db: db, nodes: nodes, signingKey: append([]byte(nil), signingKey...)}
}

func (s *NodeEnrollService) Enroll(ctx context.Context, rawToken string, input NodeEnrollInput) (NodeEnrollOutput, error) {
	payload, err := enrolltoken.Parse(rawToken, s.signingKey)
	if err != nil {
		return NodeEnrollOutput{}, ErrEnrollUnauthorized
	}
	input.Token = ""
	input.Name = strings.TrimSpace(input.Name)
	input.Hostname = strings.TrimSpace(input.Hostname)
	input.OS = strings.TrimSpace(input.OS)
	input.Arch = strings.TrimSpace(input.Arch)
	input.Version = strings.TrimSpace(input.Version)
	if input.OS == "" || input.Arch == "" {
		return NodeEnrollOutput{}, fmt.Errorf("%w: os and arch are required", ErrInvalidInput)
	}
	if input.Name == "" {
		input.Name = input.Hostname
	}
	if input.Name == "" {
		input.Name, _ = os.Hostname()
	}
	if input.Name == "" {
		return NodeEnrollOutput{}, fmt.Errorf("%w: name or hostname is required", ErrInvalidInput)
	}
	requestHash, err := hashEnrollRequest(input)
	if err != nil {
		return NodeEnrollOutput{}, err
	}

	var output NodeEnrollOutput
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var token models.NodeEnrollToken
		if err := tx.Where("id = ?", payload.TokenID).First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollUnauthorized
			}
			return fmt.Errorf("load enrollment token: %w", err)
		}
		if token.TokenHash != enrolltoken.Hash(rawToken) || token.TenantID != payload.TenantID {
			return ErrEnrollUnauthorized
		}
		if payload.NetworkID != nil {
			if token.NetworkID == nil || *token.NetworkID != *payload.NetworkID {
				return ErrEnrollUnauthorized
			}
		} else if token.NetworkID != nil {
			return ErrEnrollUnauthorized
		}
		if token.RevokedAt != nil || !token.ExpiresAt.After(time.Now().UTC()) ||
			!token.ExpiresAt.UTC().Equal(enrolltoken.ExpiresAt(payload)) {
			return ErrEnrollUnauthorized
		}

		var previous models.NodeEnrollment
		err := tx.Where("token_id = ? AND request_hash = ?", token.ID, requestHash).First(&previous).Error
		if err == nil {
			replayed, loadErr := replayOutput(ctx, tx, token, previous.NodeID, payload.Server)
			if loadErr != nil {
				return loadErr
			}
			output = replayed
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load enrollment replay: %w", err)
		}
		if token.UsedCount >= token.MaxUses {
			return ErrEnrollGone
		}
		updated := tx.Model(&models.NodeEnrollToken{}).
			Where("id = ? AND used_count < max_uses", token.ID).
			Update("used_count", gorm.Expr("used_count + 1"))
		if updated.Error != nil {
			return fmt.Errorf("consume enrollment token use: %w", updated.Error)
		}
		if updated.RowsAffected == 0 {
			return ErrEnrollGone
		}
		registered, err := s.nodes.RegisterTx(ctx, tx, token.TenantID, NodeRegisterInput{
			Name: input.Name, OS: input.OS, Arch: input.Arch, Version: input.Version, Tags: input.Tags,
		})
		if err != nil {
			return err
		}
		result := NodeEnrollOutput{
			NodeID: registered.NodeID, AgentToken: registered.AgentToken,
			PrivateKey: registered.PrivateKey, PublicKey: registered.PublicKey,
			Server: payload.Server,
		}
		if token.NetworkID != nil {
			member, memberErr := addEnrollMember(ctx, tx, *token.NetworkID, token.TenantID, registered.NodeID)
			if memberErr != nil {
				return memberErr
			}
			result.VirtualIP = member.VirtualIP
			result.NetworkID = token.NetworkID
		}
		enrollment := models.NodeEnrollment{
			ID: uuid.New(), TokenID: token.ID, NodeID: registered.NodeID,
			RequestHash: requestHash, CreatedAt: time.Now().UTC(),
		}
		if err := tx.Create(&enrollment).Error; err != nil {
			return fmt.Errorf("record enrollment use: %w", err)
		}
		output = result
		return nil
	})
	if err != nil {
		return NodeEnrollOutput{}, err
	}
	return output, nil
}

func replayOutput(ctx context.Context, tx *gorm.DB, token models.NodeEnrollToken, nodeID uuid.UUID, server string) (NodeEnrollOutput, error) {
	var node models.Node
	if err := tx.WithContext(ctx).Where("id = ? AND tenant_id = ?", nodeID, token.TenantID).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NodeEnrollOutput{}, ErrEnrollUnauthorized
		}
		return NodeEnrollOutput{}, fmt.Errorf("load enrolled node: %w", err)
	}
	result := NodeEnrollOutput{
		NodeID: node.ID, PublicKey: node.PublicKey, Server: server, Replayed: true,
	}
	if token.NetworkID != nil {
		var member models.NetworkMember
		if err := tx.WithContext(ctx).Where("network_id = ? AND node_id = ?", *token.NetworkID, node.ID).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return NodeEnrollOutput{}, fmt.Errorf("enrolled node network membership is missing")
			}
			return NodeEnrollOutput{}, fmt.Errorf("load enrolled node membership: %w", err)
		}
		result.VirtualIP = member.VirtualIP
		result.NetworkID = token.NetworkID
	}
	return result, nil
}

func addEnrollMember(ctx context.Context, tx *gorm.DB, networkID, tenantID, nodeID uuid.UUID) (models.NetworkMember, error) {
	var network models.VirtualNetwork
	if err := tx.WithContext(ctx).Where("id = ? AND tenant_id = ?", networkID, tenantID).First(&network).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.NetworkMember{}, ErrEnrollUnauthorized
		}
		return models.NetworkMember{}, fmt.Errorf("load enrollment network: %w", err)
	}
	prefix, err := netip.ParsePrefix(network.CIDR)
	if err != nil {
		return models.NetworkMember{}, fmt.Errorf("parse enrollment network CIDR: %w", err)
	}
	virtualIP, err := allocateVirtualIP(ctx, tx, network.ID, prefix)
	if err != nil {
		return models.NetworkMember{}, err
	}
	member := models.NetworkMember{
		ID: uuid.New(), NetworkID: network.ID, NodeID: nodeID,
		VirtualIP: virtualIP.String(), Role: "member", JoinedAt: time.Now().UTC(),
	}
	if err := tx.Create(&member).Error; err != nil {
		return models.NetworkMember{}, fmt.Errorf("create enrollment network member: %w", err)
	}
	return member, nil
}

func hashEnrollRequest(input NodeEnrollInput) (string, error) {
	tags := make([]string, 0, len(input.Tags))
	tags = append(tags, input.Tags...)
	value := struct {
		Name      string   `json:"name"`
		OS        string   `json:"os"`
		Arch      string   `json:"arch"`
		Version   string   `json:"version"`
		Tags      []string `json:"tags"`
		PublicKey string   `json:"public_key"`
	}{input.Name, input.OS, input.Arch, input.Version, tags, input.PublicKey}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("hash enrollment request: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
