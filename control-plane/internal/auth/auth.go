package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"neilico/control-plane/internal/models"
)

const (
	RolePlatformAdmin = "platform_admin"
	RoleTenantAdmin   = "tenant_admin"
	RoleOps           = "ops"
	RoleReadonly      = "readonly"

	TokenAccess  = "access"
	TokenRefresh = "refresh"

	AuthMethodJWT      = "jwt"
	AuthMethodAPIToken = "api_token"
)

var ErrInvalidToken = errors.New("invalid token")

type Identity struct {
	UserID   uuid.UUID `json:"user_id"`
	TenantID uuid.UUID `json:"tenant_id"`
	Role     string    `json:"role"`
}

type Claims struct {
	UserID   uuid.UUID `json:"user_id"`
	TenantID uuid.UUID `json:"tenant_id"`
	Role     string    `json:"role"`
	Type     string    `json:"token_type"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewManager(secret string, accessTTL, refreshTTL time.Duration) (*Manager, error) {
	if len(secret) < 16 {
		return nil, errors.New("JWT secret must be at least 16 bytes")
	}
	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("token TTLs must be positive")
	}
	return &Manager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}, nil
}

func (m *Manager) IssueAccess(user models.User) (string, error) {
	return m.issue(user, TokenAccess, m.accessTTL)
}

func (m *Manager) IssueRefresh(user models.User) (string, error) {
	return m.issue(user, TokenRefresh, m.refreshTTL)
}

func (m *Manager) issue(user models.User, tokenType string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   user.ID,
		TenantID: user.TenantID,
		Role:     user.Role,
		Type:     tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

func (m *Manager) Parse(tokenString, expectedType string) (Claims, error) {
	var claims Claims
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		return Claims{}, ErrInvalidToken
	}
	if claims.Type != expectedType {
		return Claims{}, ErrInvalidToken
	}
	if claims.UserID == uuid.Nil || claims.TenantID == uuid.Nil || claims.Role == "" {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

func (m *Manager) EnrollSigningKey() []byte {
	digest := sha256.Sum256([]byte("neilico-enroll-signing-key-v1\x00" + string(m.secret)))
	return digest[:]
}

func (m *Manager) CertificateEncryptionKey() []byte {
	digest := sha256.Sum256([]byte("neilico-auth-jwt-secret-v1\x00" + string(m.secret)))
	return digest[:]
}

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateAgentToken() (plain string, hash string, err error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", fmt.Errorf("generate agent token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(randomBytes)
	return plain, HashAgentToken(plain), nil
}

func HashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func AgentTokenEqual(hash, token string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashAgentToken(token))) == 1
}

func RoleAllowed(role string, allowed ...string) bool {
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}

func CanManageUsers(role string) bool {
	return RoleAllowed(role, RolePlatformAdmin, RoleTenantAdmin)
}

func CanManageNodes(role string) bool {
	return RoleAllowed(role, RolePlatformAdmin, RoleTenantAdmin, RoleOps)
}
