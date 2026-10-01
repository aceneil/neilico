package cert

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

func ParseAndMatch(certPEM, keyPEM string) (*x509.Certificate, error) {
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" || keyPEM == "" {
		return nil, errors.New("cert_pem and key_pem are required")
	}
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, errors.New("cert_pem must contain a PEM CERTIFICATE block")
	}
	leaf, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse X.509 certificate: %w", err)
	}
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, errors.New("key_pem must contain a PEM private key block")
	}
	privateKey, err := parsePrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	certPublic, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal certificate public key: %w", err)
	}
	keyPublic, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		return nil, fmt.Errorf("marshal private key public key: %w", err)
	}
	if !bytes.Equal(certPublic, keyPublic) {
		return nil, errors.New("certificate and private key do not match")
	}
	return leaf, nil
}

func parsePrivateKey(der []byte) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		switch typed := key.(type) {
		case *rsa.PrivateKey:
			return typed, nil
		case *ecdsa.PrivateKey:
			return typed, nil
		case ed25519.PrivateKey:
			return typed, nil
		}
		return nil, errors.New("unsupported PKCS#8 private key type")
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported or invalid PEM private key")
}
