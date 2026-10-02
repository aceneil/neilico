package api_test

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

func TestM2AProxyAndCertificateFlow(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			handleWebSocketUpgrade(w, r)
			return
		}
		w.Header().Set("X-Backend", "fixed")
		w.Header().Set("X-Forwarded-Host-Seen", r.Header.Get("X-Forwarded-Host"))
		w.Header().Set("X-Forwarded-Proto-Seen", r.Header.Get("X-Forwarded-Proto"))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"source":"memory-backend"}`)
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	backendHost, backendPort, err := net.SplitHostPort(backendURL.Host)
	if err != nil {
		t.Fatal(err)
	}

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "m2a", "plan": "pro"})
	requireStatus(t, status, http.StatusCreated)
	var tenant models.Tenant
	decodeResponse(t, body, &tenant)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": tenant.ID,
		"email":     "admin@m2a.test",
		"password":  "tenant-password",
		"role":      auth.RoleTenantAdmin,
		"status":    "active",
	})
	requireStatus(t, status, http.StatusCreated)
	tenantAdmin := mustLogin(t, app, "admin@m2a.test", "tenant-password")

	virtualIP := backendHost
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", tenantAdmin.Token, map[string]any{
		"name":       "proxy-node",
		"public_key": "test-public-key",
		"virtual_ip": virtualIP,
		"os":         "linux",
		"arch":       "amd64",
		"version":    "test",
	})
	requireStatus(t, status, http.StatusCreated)
	var registered struct {
		NodeID     uuid.UUID `json:"node_id"`
		AgentToken string    `json:"agent_token"`
	}
	decodeResponse(t, body, &registered)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/domains", tenantAdmin.Token, map[string]any{
		"domain": "app.example.com",
		"status": service.DomainStatusActive,
	})
	requireStatus(t, status, http.StatusCreated)
	var domain models.Domain
	decodeResponse(t, body, &domain)

	accessControl := map[string]any{
		"ip_whitelist": []string{},
		"basic_auth": map[string]any{
			"enabled":       false,
			"username":      "",
			"password_hash": "",
		},
		"require_jwt": false,
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy-rules", tenantAdmin.Token, map[string]any{
		"domain_id":      domain.ID,
		"path":           "/",
		"target_type":    "node",
		"target":         registered.NodeID.String() + ":" + backendPort,
		"access_control": accessControl,
		"enabled":        true,
	})
	requireStatus(t, status, http.StatusCreated)
	var rule models.ProxyRule
	decodeResponse(t, body, &rule)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/proxy/render", tenantAdmin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	rendered := string(body)
	if !strings.Contains(rendered, `"target_addr": "`+net.JoinHostPort(backendHost, backendPort)+`"`) ||
		!strings.Contains(rendered, `"host": "app.example.com"`) ||
		strings.Contains(rendered, registered.NodeID.String()+`":`) {
		t.Fatalf("render did not resolve node target:\n%s", rendered)
	}

	proxyServer := httptest.NewServer(app.proxy)
	defer proxyServer.Close()
	response := doProxyRequest(t, proxyServer, "/", "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d body = %s", response.StatusCode, response.Body)
	}
	if response.Body != `{"ok":true,"source":"memory-backend"}` {
		t.Fatalf("proxy body = %q", response.Body)
	}
	if response.Header.Get("X-Backend") != "fixed" ||
		response.Header.Get("X-Forwarded-Host-Seen") != "app.example.com" ||
		response.Header.Get("X-Forwarded-Proto-Seen") != "http" {
		t.Fatalf("proxy headers = %#v", response.Header)
	}
	if body := doWebSocketRequest(t, proxyServer, "/ws"); body != "socket-ready" {
		t.Fatalf("websocket body = %q", body)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("proxy-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	accessControl = map[string]any{
		"ip_whitelist": []string{"203.0.113.1/32"},
		"basic_auth": map[string]any{
			"enabled":       true,
			"username":      "proxy-user",
			"password_hash": string(passwordHash),
		},
		"require_jwt": false,
	}
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/proxy-rules/"+rule.ID.String(), tenantAdmin.Token, map[string]any{
		"domain_id":      domain.ID,
		"path":           "/",
		"target_type":    "node",
		"target":         registered.NodeID.String() + ":" + backendPort,
		"access_control": accessControl,
		"enabled":        true,
	})
	if status != http.StatusOK {
		t.Fatalf("proxy rule update status = %d body = %s", status, body)
	}
	response = doProxyRequest(t, proxyServer, "/", "", "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("whitelist status = %d body = %s", response.StatusCode, response.Body)
	}

	accessControl["ip_whitelist"] = []string{"127.0.0.0/8"}
	status, _ = mustRequest(t, app.server, http.MethodPut, "/api/v1/proxy-rules/"+rule.ID.String(), tenantAdmin.Token, map[string]any{
		"domain_id":      domain.ID,
		"path":           "/",
		"target_type":    "node",
		"target":         registered.NodeID.String() + ":" + backendPort,
		"access_control": accessControl,
		"enabled":        true,
	})
	if status != http.StatusOK {
		t.Fatalf("proxy rule update status = %d body = %s", status, body)
	}
	response = doProxyRequest(t, proxyServer, "/", "", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("basic auth missing status = %d body = %s", response.StatusCode, response.Body)
	}
	response = doProxyRequest(t, proxyServer, "/", "proxy-user", "wrong")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("basic auth wrong status = %d body = %s", response.StatusCode, response.Body)
	}
	response = doProxyRequest(t, proxyServer, "/", "proxy-user", "proxy-password")
	if response.StatusCode != http.StatusOK || response.Body != `{"ok":true,"source":"memory-backend"}` {
		t.Fatalf("basic auth valid status = %d body = %s", response.StatusCode, response.Body)
	}

	accessControl["basic_auth"] = map[string]any{"enabled": false, "username": "", "password_hash": ""}
	accessControl["require_jwt"] = true
	status, _ = mustRequest(t, app.server, http.MethodPut, "/api/v1/proxy-rules/"+rule.ID.String(), tenantAdmin.Token, map[string]any{
		"domain_id":      domain.ID,
		"path":           "/",
		"target_type":    "node",
		"target":         registered.NodeID.String() + ":" + backendPort,
		"access_control": accessControl,
		"enabled":        true,
	})
	requireStatus(t, status, http.StatusOK)
	response = doProxyRequest(t, proxyServer, "/", "", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("require_jwt missing status = %d body = %s", response.StatusCode, response.Body)
	}
	response = doProxyRequestWithToken(t, proxyServer, "/", tenantAdmin.Token)
	if response.StatusCode != http.StatusOK || response.Body != `{"ok":true,"source":"memory-backend"}` {
		t.Fatalf("require_jwt valid status = %d body = %s", response.StatusCode, response.Body)
	}

	certPEM, keyPEM := mustCertificate(t, "app.example.com")
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/certificates", tenantAdmin.Token, map[string]any{
		"cert_pem": certPEM,
		"key_pem":  keyPEM,
	})
	requireStatus(t, status, http.StatusCreated)
	if bytes.Contains(body, []byte("key_pem")) || bytes.Contains(body, []byte("PRIVATE KEY")) {
		t.Fatalf("certificate response leaked private key: %s", body)
	}
	var certificate models.Certificate
	decodeResponse(t, body, &certificate)
	if certificate.Domain != "app.example.com" || certificate.Issuer == "" || certificate.ExpiresAt == nil {
		t.Fatalf("unexpected certificate metadata: %#v", certificate)
	}
	var storedCertificate models.Certificate
	if err := app.db.First(&storedCertificate, "id = ?", certificate.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedCertificate.KeyPEM == keyPEM || !strings.HasPrefix(storedCertificate.KeyPEM, "v1:") {
		t.Fatal("private key was not stored in encrypted form")
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/certificates?domain=app.example.com", tenantAdmin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if bytes.Contains(body, []byte("key_pem")) || bytes.Contains(body, []byte("PRIVATE KEY")) {
		t.Fatalf("certificate list leaked private key: %s", body)
	}
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/domains/"+domain.ID.String(), tenantAdmin.Token, map[string]any{
		"domain":  "app.example.com",
		"cert_id": certificate.ID,
		"status":  "disabled",
	})
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &domain)
	if domain.Status != service.DomainStatusActive {
		t.Fatalf("certificate-bound domain status = %q, want active", domain.Status)
	}

	_, otherKey := mustCertificate(t, "app.example.com")
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/certificates", tenantAdmin.Token, map[string]any{
		"cert_pem": certPEM,
		"key_pem":  otherKey,
	})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/"+registered.NodeID.String()+"/traffic", registered.AgentToken, []map[string]any{
		{"direction": "in", "bytes": 128, "protocol": "tcp", "peer": "203.0.113.5:443"},
		{"direction": "out", "bytes": 256, "protocol": "tcp", "peer": "203.0.113.5:443"},
	})
	requireStatus(t, status, http.StatusCreated)
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/traffic?node_id="+registered.NodeID.String()+"&page=1", tenantAdmin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var traffic service.TrafficList
	decodeResponse(t, body, &traffic)
	if traffic.Total != 2 || len(traffic.Items) != 2 {
		t.Fatalf("unexpected traffic list: %#v", traffic)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/proxy/providers", tenantAdmin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	if !bytes.Contains(body, []byte(`"configured_kind":"builtin"`)) || !bytes.Contains(body, []byte(`"status":"up"`)) {
		t.Fatalf("provider status response = %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/metrics", "", nil)
	requireStatus(t, status, http.StatusOK)
	if !bytes.Contains(body, []byte("neilico_proxy_requests_total")) || !bytes.Contains(body, []byte("neilico_proxy_provider_up")) {
		t.Fatalf("metrics missing proxy metrics:\n%s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "m2a-other", "plan": "free"})
	requireStatus(t, status, http.StatusCreated)
	var otherTenant models.Tenant
	decodeResponse(t, body, &otherTenant)
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID,
		"email":     "admin@other.test",
		"password":  "other-password",
		"role":      auth.RoleTenantAdmin,
	})
	requireStatus(t, status, http.StatusCreated)
	otherAdmin := mustLogin(t, app, "admin@other.test", "other-password")
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/domains", otherAdmin.Token, map[string]any{
		"domain": "app.example.com",
		"status": service.DomainStatusActive,
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "conflict")
	for _, path := range []string{
		"/api/v1/domains/" + domain.ID.String(),
		"/api/v1/proxy-rules/" + rule.ID.String(),
	} {
		status, body = mustRequest(t, app.server, http.MethodGet, path, otherAdmin.Token, nil)
		if status != http.StatusNotFound && status != http.StatusForbidden {
			t.Fatalf("cross-tenant GET %s status = %d body = %s", path, status, body)
		}
		if bytes.Contains(body, []byte("app.example.com")) {
			t.Fatalf("cross-tenant GET %s leaked data: %s", path, body)
		}
	}
}

type proxyResponse struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func doProxyRequest(t *testing.T, server *httptest.Server, path, username, password string) proxyResponse {
	return doProxyRequestWithAuthorization(t, server, path, func(request *http.Request) {
		if username != "" {
			request.SetBasicAuth(username, password)
		}
	})
}

func doProxyRequestWithToken(t *testing.T, server *httptest.Server, path, token string) proxyResponse {
	return doProxyRequestWithAuthorization(t, server, path, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+token)
	})
}

func doProxyRequestWithAuthorization(t *testing.T, server *httptest.Server, path string, authorize func(*http.Request)) proxyResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "app.example.com"
	authorize(request)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return proxyResponse{StatusCode: response.StatusCode, Header: response.Header, Body: string(body)}
}

func handleWebSocketUpgrade(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || key == "" {
		http.Error(w, "websocket required", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer connection.Close()
	acceptSum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	_, _ = fmt.Fprintf(connection, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\nsocket-ready", base64.StdEncoding.EncodeToString(acceptSum[:]))
}

func doWebSocketRequest(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", serverURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	key := base64.StdEncoding.EncodeToString([]byte("neilico-websocket-test"))
	_, err = fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: app.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, key)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("websocket status = %d", response.StatusCode)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func mustCertificate(t *testing.T, domain string) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
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
	return fmt.Sprintf("%s", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
