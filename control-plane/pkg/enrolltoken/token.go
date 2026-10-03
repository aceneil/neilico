package enrolltoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const Prefix = "neilico-enroll."

var (
	ErrInvalidToken   = errors.New("invalid enrollment token")
	ErrInvalidPayload = errors.New("invalid enrollment token payload")
)

type Payload struct {
	Version   int        `json:"v"`
	Server    string     `json:"srv"`
	TenantID  uuid.UUID  `json:"tid"`
	NetworkID *uuid.UUID `json:"nid,omitempty"`
	TokenID   uuid.UUID  `json:"jti"`
	ExpiresAt int64      `json:"exp"`
}

func Sign(payload Payload, key []byte) (string, error) {
	if err := validatePayload(payload); err != nil {
		return "", err
	}
	if len(key) == 0 {
		return "", errors.New("enrollment signing key is required")
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode enrollment token payload: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(encodedPayload)
	signature := mac.Sum(nil)
	return Prefix + base64.RawURLEncoding.EncodeToString(encodedPayload) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

// Inspect parses and validates the token format without verifying its HMAC.
// It is intended only for agent-side server discovery before authentication.
func Inspect(token string) (Payload, error) {
	payloadBytes, _, err := split(token)
	if err != nil {
		return Payload{}, err
	}
	var payload Payload
	if err := decodePayload(payloadBytes, &payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func Parse(token string, key []byte) (Payload, error) {
	payloadBytes, signature, err := split(token)
	if err != nil {
		return Payload{}, err
	}
	if len(key) == 0 {
		return Payload{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payloadBytes)
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(expected, signature) != 1 {
		return Payload{}, ErrInvalidToken
	}
	var payload Payload
	if err := decodePayload(payloadBytes, &payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func split(token string) ([]byte, []byte, error) {
	if !strings.HasPrefix(token, Prefix) {
		return nil, nil, ErrInvalidToken
	}
	parts := strings.Split(strings.TrimPrefix(token, Prefix), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil, ErrInvalidToken
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payloadBytes) == 0 || len(payloadBytes) > 4096 {
		return nil, nil, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return nil, nil, ErrInvalidToken
	}
	return payloadBytes, signature, nil
}

func decodePayload(raw []byte, payload *Payload) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(payload); err != nil {
		return ErrInvalidPayload
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return ErrInvalidPayload
	}
	return validatePayload(*payload)
}

func validatePayload(payload Payload) error {
	if payload.Version != 1 || payload.Server == "" || payload.TenantID == uuid.Nil ||
		payload.TokenID == uuid.Nil || payload.ExpiresAt <= 0 {
		return ErrInvalidPayload
	}
	parsed, err := url.Parse(payload.Server)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil {
		return ErrInvalidPayload
	}
	if payload.NetworkID != nil && *payload.NetworkID == uuid.Nil {
		return ErrInvalidPayload
	}
	return nil
}

func ExpiresAt(payload Payload) time.Time {
	return time.Unix(payload.ExpiresAt, 0).UTC()
}
