package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service/cert"
)

func TestBuiltinSNISelectsExactCertificate(t *testing.T) {
	t.Parallel()
	handle := newProxyDB(t)
	crypto, err := cert.NewCryptoFromKey([]byte("tls-key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"one.example.test", "two.example.test"} {
		certPEM, keyPEM := proxyCertificate(t, domain)
		encrypted, err := crypto.Encrypt(keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		expiry := time.Now().Add(24 * time.Hour)
		if err := handle.Create(&models.Certificate{
			ID: uuid.New(), TenantID: uuid.New(), Domain: domain, Issuer: "manual",
			CertPEM: certPEM, KeyPEM: encrypted, ExpiresAt: &expiry, Status: "active", AutoRenew: false,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	builtin := NewBuiltin(handle, nil, nil, nil)
	builtin.ConfigureCertificateSource(crypto, nil)
	one, err := builtin.GetCertificate(&tls.ClientHelloInfo{ServerName: "one.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := builtin.GetCertificate(&tls.ClientHelloInfo{ServerName: "two.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if one.Leaf.Subject.CommonName != "one.example.test" || two.Leaf.Subject.CommonName != "two.example.test" {
		t.Fatalf("SNI selected wrong leaves: %q and %q", one.Leaf.Subject.CommonName, two.Leaf.Subject.CommonName)
	}
	if _, err := builtin.GetCertificate(&tls.ClientHelloInfo{ServerName: "unknown.example.test"}); err == nil {
		t.Fatal("unknown SNI unexpectedly returned a certificate")
	}
}

func TestBuiltinCertificateCacheInvalidation(t *testing.T) {
	t.Parallel()
	handle := newProxyDB(t)
	crypto, _ := cert.NewCryptoFromKey([]byte("cache-key"))
	firstPEM, firstKey := proxyCertificate(t, "cache.example.test")
	secondPEM, secondKey := proxyCertificate(t, "cache.example.test")
	firstEncrypted, _ := crypto.Encrypt(firstKey)
	secondEncrypted, _ := crypto.Encrypt(secondKey)
	expiry := time.Now().Add(24 * time.Hour)
	item := models.Certificate{
		ID: uuid.New(), TenantID: uuid.New(), Domain: "cache.example.test", Issuer: "manual",
		CertPEM: firstPEM, KeyPEM: firstEncrypted, ExpiresAt: &expiry, Status: "active",
	}
	if err := handle.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	builtin := NewBuiltin(handle, nil, nil, nil)
	builtin.ConfigureCertificateSource(crypto, nil)
	first, err := builtin.GetCertificate(&tls.ClientHelloInfo{ServerName: "cache.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Leaf.SerialNumber.String() == "" {
		t.Fatal("first certificate missing serial")
	}
	if err := handle.Model(&models.Certificate{}).Where("id = ?", item.ID).Updates(map[string]any{"cert_pem": secondPEM, "key_pem": secondEncrypted}).Error; err != nil {
		t.Fatal(err)
	}
	cached, err := builtin.GetCertificate(&tls.ClientHelloInfo{ServerName: "cache.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if cached.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) != 0 {
		t.Fatal("cache was not used before explicit invalidation")
	}
	builtin.InvalidateCertificate("cache.example.test")
	renewed, err := builtin.GetCertificate(&tls.ClientHelloInfo{ServerName: "cache.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatal("invalidated cache did not reload replacement certificate")
	}
}

func TestBuiltinChallengePathHasPrecedenceOverProxy(t *testing.T) {
	t.Parallel()
	handle := newProxyDB(t)
	builtin := NewBuiltin(handle, nil, nil, nil)
	challenge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("challenge-value"))
	})
	builtin.ConfigureCertificateSource(nil, challenge)
	request := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/token", nil)
	request.Host = ""
	recorder := httptest.NewRecorder()
	builtin.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "challenge-value" {
		t.Fatalf("challenge response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func newProxyDB(t *testing.T) *gorm.DB {
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
	return handle
}

func proxyCertificate(t *testing.T, domain string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
