package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	APITokenPrefix       = "umpp_"
	APITokenPrefixLength = 8
)

var ErrInvalidScope = errors.New("invalid scope")

func GenerateAPIToken() (plain, hash, prefix string, err error) {
	randomBytes := make([]byte, 32)
	if _, err = rand.Read(randomBytes); err != nil {
		return "", "", "", fmt.Errorf("generate API token: %w", err)
	}
	plain = APITokenPrefix + base64.RawURLEncoding.EncodeToString(randomBytes)
	return plain, HashAPIToken(plain), APITokenDisplayPrefix(plain), nil
}

func HashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func IsAPIToken(token string) bool {
	return strings.HasPrefix(token, APITokenPrefix)
}

func APITokenDisplayPrefix(token string) string {
	if len(token) <= APITokenPrefixLength {
		return token
	}
	return token[:APITokenPrefixLength]
}

func RedactAPIToken(token string) string {
	if token == "" {
		return ""
	}
	return APITokenDisplayPrefix(token) + "\u2026"
}
