package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestCryptoRoundTrip(t *testing.T) {
	t.Parallel()
	crypto, err := NewCrypto("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := crypto.Encrypt("private-key-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "private-key-plaintext") {
		t.Fatal("encrypted key contains plaintext")
	}
	decrypted, err := crypto.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "private-key-plaintext" {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestParseAndMatch(t *testing.T) {
	t.Parallel()
	certPEM, keyPEM := mustSelfSigned(t, "app.example.com")
	if _, err := ParseAndMatch(certPEM, keyPEM); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}
	_, otherKey := mustSelfSigned(t, "app.example.com")
	if _, err := ParseAndMatch(certPEM, otherKey); err == nil {
		t.Fatal("mismatched certificate and key accepted")
	}
}

func TestACMEIssuerIsReserved(t *testing.T) {
	t.Parallel()
	if _, err := (ACMEIssuer{}).Issue(t.Context(), "app.example.com"); err != ErrNotImplemented {
		t.Fatalf("ACME error = %v, want %v", err, ErrNotImplemented)
	}
}

func mustSelfSigned(t *testing.T, domain string) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
