package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/metrics"
	certcrypto "umpp/control-plane/internal/service/cert"
	"umpp/control-plane/internal/service/pki"
)

func TestRequireClientCertificateExemptsHealthAndMetrics(t *testing.T) {
	t.Parallel()
	observer := metrics.New(nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/metrics" && r.URL.Path != "/api/v1/pki/ca" && len(r.TLS.VerifiedChains) == 0 {
			t.Error("protected handler reached without verified client chain")
		}
		w.WriteHeader(http.StatusOK)
	})
	server := newTLSServer(t, requireClientCertificate(next, "require", observer), nil, nil)
	defer server.Close()
	for _, path := range []string{"/healthz", "/metrics", "/api/v1/pki/ca"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, response.StatusCode)
		}
	}
	response, err := server.Client().Get(server.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated protected request = %d", response.StatusCode)
	}
}

func TestRealTLSClientAuthentication(t *testing.T) {
	t.Parallel()
	pkiService, handle := testPKI(t, true)
	issued, err := pkiService.IssueNodeCert(uuid.New(), "valid-node")
	if err != nil {
		t.Fatal(err)
	}
	other := independentClientCertificate(t)
	expired := expiredClientCertificate(t)
	serverCert, err := pkiService.ServerCertificate([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pkiService.ClientCACertPool()
	if err != nil {
		t.Fatal(err)
	}
	observer := metrics.New(handle)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 && r.URL.Path != "/healthz" {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := newTLSServer(t, requireClientCertificate(next, "require", observer), &serverCert, pool)
	defer server.Close()

	cases := []struct {
		name    string
		certPEM string
		keyPEM  string
		status  int
	}{
		{name: "valid", certPEM: issued.CertPEM, keyPEM: issued.KeyPEM, status: http.StatusOK},
		{name: "other-ca", certPEM: other.certPEM, keyPEM: other.keyPEM, status: http.StatusUnauthorized},
		{name: "expired", certPEM: expired.certPEM, keyPEM: expired.keyPEM, status: http.StatusUnauthorized},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			client := tlsClient(t, server, item.certPEM, item.keyPEM)
			response, err := client.Get(server.URL + "/api/v1/nodes")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != item.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, item.status)
			}
		})
	}
}

func TestPKIDisabledDoesNotBuildMTLSConfiguration(t *testing.T) {
	t.Parallel()
	_, handle := testPKI(t, false)
	cfg := config.Default()
	cfg.Server.TLS.Enabled = true
	cfg.Server.TLS.ClientAuth = "require"
	cfg.PKI.Enabled = false
	observer := metrics.New(handle)
	if _, err := apiServerTLSConfig(cfg, nil, observer); err == nil || !strings.Contains(err.Error(), "cert_file") {
		t.Fatalf("mTLS without PKI/client CA error = %v", err)
	}
}

func testPKI(t *testing.T, enabled bool) (*pki.Service, *gorm.DB) {
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
	crypto, err := certcrypto.NewCrypto("tls-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return pki.New(handle, crypto, pki.Options{Enabled: enabled, CommonName: "TLS Test CA", ServerCertDays: 30, NodeCertDays: 10}, nil), handle
}

func newTLSServer(t *testing.T, handler http.Handler, certificate *tls.Certificate, pool *x509.CertPool) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if certificate != nil {
		config.Certificates = []tls.Certificate{*certificate}
	}
	if pool != nil {
		config.ClientCAs = pool
		config.ClientAuth = tls.VerifyClientCertIfGiven
	}
	server.TLS = config
	if pool != nil {
		config.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return nil
			}
			_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
				Roots:     pool,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			})
			return err
		}
	}
	server.StartTLS()
	return server
}

func tlsClient(t *testing.T, server *httptest.Server, certPEM, keyPEM string) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}
	if certPEM != "" {
		pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			t.Fatal(err)
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	}
	return &http.Client{Transport: transport}
}

type testCertificate struct {
	certPEM string
	keyPEM  string
}

func independentClientCertificate(t *testing.T) testCertificate {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caCert, issuer := makeIssuer(t, caKey, "Other CA")
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := makeIssued(t, leafKey, issuer, "other-node", false, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	_ = caCert
	return leaf
}

func expiredClientCertificate(t *testing.T) testCertificate {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, issuer := makeIssuer(t, caKey, "Expired Client CA")
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return makeIssued(t, leafKey, issuer, "expired-node", false, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour), []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
}

type issuerCertificate struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func makeIssuer(t *testing.T, key *ecdsa.PrivateKey, commonName string) (testCertificate, *issuerCertificate) {
	t.Helper()
	item := makeSelfSigned(t, key, commonName, true, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), nil)
	block, _ := pem.Decode([]byte(item.certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return item, &issuerCertificate{cert: cert, key: key}
}

func makeIssued(t *testing.T, key *ecdsa.PrivateKey, issuer *issuerCertificate, commonName string, isCA bool, notBefore, notAfter time.Time, eku []x509.ExtKeyUsage) testCertificate {
	t.Helper()
	return makeCertificate(t, key, issuer, commonName, isCA, notBefore, notAfter, eku)
}

func makeSelfSigned(t *testing.T, key *ecdsa.PrivateKey, commonName string, isCA bool, notBefore, notAfter time.Time, eku []x509.ExtKeyUsage) testCertificate {
	t.Helper()
	return makeCertificate(t, key, nil, commonName, isCA, notBefore, notAfter, eku)
}

func makeCertificate(t *testing.T, key *ecdsa.PrivateKey, issuer *issuerCertificate, commonName string, isCA bool, notBefore, notAfter time.Time, eku []x509.ExtKeyUsage) testCertificate {
	t.Helper()
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName},
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, IsCA: isCA,
		ExtKeyUsage: eku,
	}
	if isCA {
		template.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	}
	parent := template
	signer := key
	if issuer != nil {
		parent, signer = issuer.cert, issuer.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return testCertificate{
		certPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		keyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
	}
}
