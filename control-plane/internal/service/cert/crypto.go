package cert

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const encryptedKeyPrefix = "v1:"

// Crypto encrypts private keys with AES-GCM using a key derived from auth.jwt_secret.
type Crypto struct {
	aead cipher.AEAD
}

func NewCrypto(jwtSecret string) (*Crypto, error) {
	if strings.TrimSpace(jwtSecret) == "" {
		return nil, errors.New("jwt secret is required for certificate key encryption")
	}
	digest := sha256.Sum256([]byte("neilico-certificate-key-v1\x00" + jwtSecret))
	return NewCryptoFromKey(digest[:])
}

func NewCryptoFromKey(key []byte) (*Crypto, error) {
	if len(key) == 0 {
		return nil, errors.New("certificate encryption key is required")
	}
	digest := sha256.Sum256(append([]byte("neilico-certificate-key-v1\x00"), key...))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, fmt.Errorf("create certificate cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create certificate AEAD: %w", err)
	}
	return &Crypto{aead: aead}, nil
}

func (c *Crypto) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate certificate nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedKeyPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *Crypto) Decrypt(encoded string) (string, error) {
	if !strings.HasPrefix(encoded, encryptedKeyPrefix) {
		return "", errors.New("invalid encrypted certificate key format")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, encryptedKeyPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted certificate key: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("encrypted certificate key is too short")
	}
	plaintext, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt certificate key: %w", err)
	}
	return string(plaintext), nil
}
