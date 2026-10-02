package pki

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

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	certcrypto "neilico/control-plane/internal/service/cert"
)

func newTestService(t *testing.T, enabled bool) (*Service, *gorm.DB) {
	t.Helper()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared"}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := handle.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	crypto, err := certcrypto.NewCrypto("test-jwt-secret")
	if err != nil {
		t.Fatal(err)
	}
	return New(handle, crypto, Options{Enabled: enabled, CommonName: "NEILICO Test CA", ServerHosts: []string{"api.example.test"}, ServerCertDays: 30, NodeCertDays: 10, RenewBeforeDays: 2}, nil), handle
}

func TestEnsureCAEncryptsAndIsIdempotent(t *testing.T) {
	service, handle := newTestService(t, true)
	first, err := service.EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.EncryptedKeyPEM, "PRIVATE KEY") || first.EncryptedKeyPEM == "" {
		t.Fatal("CA private key was not encrypted at rest")
	}
	second, err := service.EnsureCA()
	if err != nil || second.ID != first.ID {
		t.Fatalf("EnsureCA was not idempotent: %#v %v", second, err)
	}
	var count int64
	if err := handle.Raw("SELECT COUNT(*) FROM cas").Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("unexpected CA count %d/%v", count, err)
	}
}

func TestIssueCertificatesSANAndEKUAndVerification(t *testing.T) {
	service, _ := newTestService(t, true)
	server, err := service.IssueServerCert([]string{"public.example.test:8443"})
	if err != nil {
		t.Fatal(err)
	}
	serverCert := parseCert(t, server.CertPEM)
	if err := serverCert.VerifyHostname("public.example.test"); err != nil {
		t.Fatalf("server SAN missing host: %v", err)
	}
	if err := serverCert.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("server SAN missing loopback: %v", err)
	}
	nodeID := uuid.New()
	node, err := service.IssueNodeCert(nodeID, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	nodeCert := parseCert(t, node.CertPEM)
	if len(nodeCert.ExtKeyUsage) != 1 || nodeCert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("node EKU = %#v", nodeCert.ExtKeyUsage)
	}
	if err := service.VerifyNodeCert(nodeCert); err != nil {
		t.Fatalf("valid node certificate rejected: %v", err)
	}
	if !strings.Contains(nodeCert.DNSNames[0], nodeID.String()) && !strings.Contains(nodeCert.URIs[0].String(), nodeID.String()) {
		t.Fatalf("node certificate omitted node UUID SAN: %#v", nodeCert)
	}
	if _, err := service.IssueNodeCert(uuid.Nil, "bad"); err == nil {
		t.Fatal("nil node ID accepted")
	}
}

func TestExpiredCertificateAndOldRootTrust(t *testing.T) {
	service, _ := newTestService(t, true)
	old, err := service.EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	issued, err := service.IssueNodeCert(nodeID, "old-node")
	if err != nil {
		t.Fatal(err)
	}
	oldCert := parseCert(t, issued.CertPEM)
	rotated, err := service.RotateCA()
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ID == old.ID {
		t.Fatal("CA rotation reused the old CA")
	}
	if err := service.VerifyNodeCert(oldCert); err != nil {
		t.Fatalf("old root trust anchor lost after rotation: %v", err)
	}
	// An expired leaf signed by the current root must fail despite a valid chain.
	ca, err := service.ActiveCA()
	if err != nil {
		t.Fatal(err)
	}
	caCert, caKey, err := service.caMaterial(ca)
	if err != nil {
		t.Fatal(err)
	}
	expired := signExpiredClient(t, caCert, caKey)
	if err := service.VerifyNodeCert(expired); err == nil {
		t.Fatal("expired node certificate was accepted")
	}
}

func TestDisabledReturnsReadableError(t *testing.T) {
	service, _ := newTestService(t, false)
	if _, err := service.EnsureCA(); err != ErrDisabled {
		t.Fatalf("EnsureCA error = %v", err)
	}
	if _, err := service.IssueNodeCert(uuid.New(), "node"); err != ErrDisabled {
		t.Fatalf("IssueNodeCert error = %v", err)
	}
}

func parseCert(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func signExpiredClient(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	// The fixture is intentionally generated with a tiny validity window in the
	// past; using the service key type keeps this test focused on verification.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "expired"},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(-time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}, caCert, key.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
