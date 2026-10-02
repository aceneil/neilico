package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	appconfig "neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service/cert"
	acmeclient "neilico/control-plane/internal/service/cert/acme"
)

type lifecycleTransport struct {
	mu       sync.Mutex
	calls    int
	started  chan struct{}
	release  chan struct{}
	certPEM  string
	keyPEM   string
	notAfter time.Time
	err      error
}

func (f *lifecycleTransport) Register(context.Context, string) error { return nil }

func (f *lifecycleTransport) Obtain(context.Context, string, acmeclient.ChallengeSolver) ([]byte, []byte, time.Time, error) {
	f.mu.Lock()
	f.calls++
	started := f.started
	release := f.release
	f.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		<-release
	}
	if f.err != nil {
		return nil, nil, time.Time{}, f.err
	}
	return []byte(f.certPEM), []byte(f.keyPEM), f.notAfter, nil
}

func (f *lifecycleTransport) Revoke(context.Context, []byte) error {
	return acmeclient.ErrNotImplemented
}

func (f *lifecycleTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type lifecycleMetrics struct {
	mu          sync.Mutex
	orderResult string
	renewResult string
	expiry      map[string]float64
}

func (m *lifecycleMetrics) ObserveACMEOrder(result string, _ time.Duration) {
	m.mu.Lock()
	m.orderResult = result
	m.mu.Unlock()
}

func (m *lifecycleMetrics) ObserveCertificateRenewal(result string) {
	m.mu.Lock()
	m.renewResult = result
	m.mu.Unlock()
}

func (m *lifecycleMetrics) SetCertificateExpiry(domain string, days float64) {
	m.mu.Lock()
	if m.expiry == nil {
		m.expiry = make(map[string]float64)
	}
	m.expiry[domain] = days
	m.mu.Unlock()
}

type lifecycleBumper struct {
	mu    sync.Mutex
	proxy int
	node  int
}

func (b *lifecycleBumper) BumpForProxy(context.Context, uuid.UUID, string) (models.ConfigVersion, error) {
	b.mu.Lock()
	b.proxy++
	b.mu.Unlock()
	return models.ConfigVersion{}, nil
}

func (b *lifecycleBumper) BumpForNode(context.Context, uuid.UUID, string) (models.ConfigVersion, error) {
	b.mu.Lock()
	b.node++
	b.mu.Unlock()
	return models.ConfigVersion{}, nil
}

type lifecycleInvalidator struct {
	mu      sync.Mutex
	domains []string
}

func (i *lifecycleInvalidator) InvalidateCertificate(domain string) {
	i.mu.Lock()
	i.domains = append(i.domains, domain)
	i.mu.Unlock()
}

func TestACMEClientDisabledMakesNoOrder(t *testing.T) {
	t.Parallel()
	handle := newLifecycleDB(t)
	transport := &lifecycleTransport{}
	client := acmeclient.New(acmeclient.Config{Enabled: false, Challenge: acmeclient.ChallengeHTTP01}, transport)
	crypto, _ := cert.NewCryptoFromKey([]byte("lifecycle-key"))
	service := NewCertificateService(handle, crypto)
	service.ConfigureACME(client, ACMEOptions{ChallengeSolver: acmeclient.NewChallengeStore(time.Hour)}, nil, nil, nil, nil)
	_, err := service.RequestACME(context.Background(), uuid.New(), CertificateInput{Issuer: "acme", Domain: "app.example.test"})
	if err == nil || acmeclient.ErrorCode(err) != "acme_disabled" {
		t.Fatalf("RequestACME error = %v", err)
	}
	if transport.count() != 0 {
		t.Fatalf("transport calls = %d, want 0", transport.count())
	}
}

func TestSingleFlightAndSuccessfulACMEIssuance(t *testing.T) {
	t.Parallel()
	handle := newLifecycleDB(t)
	certPEM, keyPEM := lifecycleCertificate(t, "app.example.test")
	transport := &lifecycleTransport{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		certPEM:  certPEM,
		keyPEM:   keyPEM,
		notAfter: time.Now().Add(48 * time.Hour),
	}
	client := acmeclient.New(acmeclient.Config{Enabled: true, AgreeTOS: true, Challenge: acmeclient.ChallengeHTTP01}, transport)
	crypto, _ := cert.NewCryptoFromKey([]byte("lifecycle-key"))
	metrics := &lifecycleMetrics{}
	bumper := &lifecycleBumper{}
	invalidator := &lifecycleInvalidator{}
	service := NewCertificateService(handle, crypto)
	service.ConfigureACME(client, ACMEOptions{ChallengeSolver: acmeclient.NewChallengeStore(time.Hour)}, metrics, bumper, invalidator, nil)
	item, err := service.RequestACME(context.Background(), uuid.New(), CertificateInput{Issuer: "acme", Domain: "app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	<-transport.started
	if _, err := service.Renew(context.Background(), item.ID, nil); !errors.Is(err, ErrOrderInFlight) {
		t.Fatalf("second order error = %v, want ErrOrderInFlight", err)
	}
	close(transport.release)
	waitForCertificate(t, handle, item.ID, CertificateStatusActive)
	var stored models.Certificate
	if err := handle.First(&stored, "id = ?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.After(time.Now()) || stored.RenewCount != 0 || stored.LastError != "" {
		t.Fatalf("successful issuance state = %#v", stored)
	}
	if transport.count() != 1 {
		t.Fatalf("order calls = %d, want 1", transport.count())
	}
	if metrics.orderResult != "success" || metrics.expiry["app.example.test"] <= 0 {
		t.Fatalf("metrics = %#v", metrics)
	}
	if len(invalidator.domains) != 1 || invalidator.domains[0] != "app.example.test" {
		t.Fatalf("invalidated domains = %#v", invalidator.domains)
	}
}

func TestRenewalFailureKeepsActiveAndBacksOff(t *testing.T) {
	t.Parallel()
	handle := newLifecycleDB(t)
	certPEM, keyPEM := lifecycleCertificate(t, "app.example.test")
	crypto, _ := cert.NewCryptoFromKey([]byte("lifecycle-key"))
	encrypted, err := crypto.Encrypt(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(24 * time.Hour)
	item := models.Certificate{
		ID: uuid.New(), TenantID: uuid.New(), Domain: "app.example.test", Issuer: "acme",
		CertPEM: certPEM, KeyPEM: encrypted, ExpiresAt: &expiry, Status: CertificateStatusActive,
		ChallengeType: acmeclient.ChallengeHTTP01, AutoRenew: true,
	}
	if err := handle.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	transport := &lifecycleTransport{err: errors.New("validation failed")}
	client := acmeclient.New(acmeclient.Config{Enabled: true, AgreeTOS: true, Challenge: acmeclient.ChallengeHTTP01}, transport)
	metrics := &lifecycleMetrics{}
	service := NewCertificateService(handle, crypto)
	service.ConfigureACME(client, ACMEOptions{ChallengeSolver: acmeclient.NewChallengeStore(time.Hour)}, metrics, nil, nil, nil)
	if _, err := service.Renew(context.Background(), item.ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var stored models.Certificate
		if err := handle.First(&stored, "id = ?", item.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.LastError != "" {
			if stored.Status != CertificateStatusActive || stored.NextAttemptAt == nil || !stored.NextAttemptAt.After(time.Now()) || stored.RenewalFailures != 1 {
				t.Fatalf("failed renewal state = %#v", stored)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for renewal failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if metrics.orderResult != "failure" || metrics.renewResult != "failure" {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestShouldRenewDecisionMatrix(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	future := now.Add(60 * 24 * time.Hour)
	threshold := now.Add(10 * 24 * time.Hour)
	expired := now.Add(-time.Hour)
	for _, tc := range []struct {
		name string
		item models.Certificate
		want bool
	}{
		{"outside threshold", models.Certificate{Issuer: "acme", Status: "active", AutoRenew: true, ExpiresAt: &future}, false},
		{"inside threshold", models.Certificate{Issuer: "acme", Status: "active", AutoRenew: true, ExpiresAt: &threshold}, true},
		{"expired", models.Certificate{Issuer: "acme", Status: "active", AutoRenew: true, ExpiresAt: &expired}, true},
		{"auto renew false", models.Certificate{Issuer: "acme", Status: "active", AutoRenew: false, ExpiresAt: &future}, false},
		{"manual certificate", models.Certificate{Issuer: "manual", Status: "active", AutoRenew: true, ExpiresAt: &threshold}, false},
		{"failed status", models.Certificate{Issuer: "acme", Status: "failed", AutoRenew: true, ExpiresAt: &threshold}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldRenew(tc.item, now, 30*24*time.Hour, true); got != tc.want {
				t.Fatalf("ShouldRenew = %v, want %v", got, tc.want)
			}
		})
	}
}

func newLifecycleDB(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := db.Open(configForLifecycle(), "error")
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

func configForLifecycle() appconfig.Database {
	return appconfig.Database{Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared"}
}

func lifecycleCertificate(t *testing.T, domain string) (string, string) {
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

func waitForCertificate(t *testing.T, handle *gorm.DB, id uuid.UUID, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var item models.Certificate
		if err := handle.First(&item, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if item.Status == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for certificate status %q", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRenewalSuccessUpdatesLifecycleAndInvalidatesCache(t *testing.T) {
	t.Parallel()
	handle := newLifecycleDB(t)
	oldCertPEM, oldKeyPEM := lifecycleCertificate(t, "renew.example.test")
	newCertPEM, newKeyPEM := lifecycleCertificate(t, "renew.example.test")
	crypto, _ := cert.NewCryptoFromKey([]byte("renewal-key"))
	encrypted, _ := crypto.Encrypt(oldKeyPEM)
	expiry := time.Now().Add(24 * time.Hour)
	item := models.Certificate{
		ID: uuid.New(), TenantID: uuid.New(), Domain: "renew.example.test", Issuer: "acme",
		CertPEM: oldCertPEM, KeyPEM: encrypted, ExpiresAt: &expiry, Status: CertificateStatusActive,
		ChallengeType: acmeclient.ChallengeHTTP01, AutoRenew: true,
	}
	if err := handle.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	transport := &lifecycleTransport{
		certPEM:  newCertPEM,
		keyPEM:   newKeyPEM,
		notAfter: time.Now().Add(60 * 24 * time.Hour),
	}
	client := acmeclient.New(acmeclient.Config{Enabled: true, AgreeTOS: true, Challenge: acmeclient.ChallengeHTTP01}, transport)
	invalidator := &lifecycleInvalidator{}
	service := NewCertificateService(handle, crypto)
	service.ConfigureACME(client, ACMEOptions{ChallengeSolver: acmeclient.NewChallengeStore(time.Hour)}, &lifecycleMetrics{}, &lifecycleBumper{}, invalidator, nil)
	if _, err := service.Renew(context.Background(), item.ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var stored models.Certificate
		if err := handle.First(&stored, "id = ?", item.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.RenewCount == 1 {
			if strings.TrimSpace(stored.CertPEM) != strings.TrimSpace(newCertPEM) {
				t.Fatal("renewal did not replace cert_pem")
			}
			if stored.RenewedAt == nil || stored.ExpiresAt == nil || !stored.ExpiresAt.After(expiry) || stored.LastError != "" {
				t.Fatal("renewal lifecycle fields were not updated")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for renewal: %#v", stored)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(invalidator.domains) != 1 || invalidator.domains[0] != "renew.example.test" {
		t.Fatalf("cache invalidations = %#v", invalidator.domains)
	}
}
