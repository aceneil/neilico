package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/models"
	certservice "umpp/control-plane/internal/service/cert"
	acmepkg "umpp/control-plane/internal/service/cert/acme"
)

type Builtin struct {
	db            *gorm.DB
	auth          *auth.Manager
	observer      Observer
	logger        *slog.Logger
	routes        atomic.Pointer[RouteSet]
	mu            sync.RWMutex
	state         State
	certCrypto    *certservice.Crypto
	challenge     http.Handler
	certificateMu sync.RWMutex
	certificates  map[string]cachedCertificate
}

type cachedCertificate struct {
	certificate tls.Certificate
	expiresAt   time.Time
}

func NewBuiltin(db *gorm.DB, authManager *auth.Manager, logger *slog.Logger, observer Observer) *Builtin {
	if logger == nil {
		logger = slog.Default()
	}
	provider := &Builtin{
		db:           db,
		auth:         authManager,
		observer:     observer,
		logger:       logger,
		state:        State{Kind: "builtin", Status: "unknown", UpdatedAt: time.Now().UTC()},
		certificates: make(map[string]cachedCertificate),
	}
	provider.routes.Store(&RouteSet{byHost: map[string][]Route{}, Routes: []Route{}})
	return provider
}

func (p *Builtin) Kind() string { return "builtin" }

func (p *Builtin) Render(ctx context.Context, tenantID uuid.UUID) ([]byte, error) {
	set, err := LoadRoutes(ctx, p.db)
	if err != nil {
		return nil, err
	}
	if tenantID != uuid.Nil {
		filtered := &RouteSet{byHost: map[string][]Route{}, Routes: []Route{}}
		for _, route := range set.Routes {
			if route.TenantID == tenantID {
				filtered.Routes = append(filtered.Routes, route)
				filtered.byHost[route.Host] = append(filtered.byHost[route.Host], route)
			}
		}
		for host := range filtered.byHost {
			routes := filtered.byHost[host]
			sortRoutes(routes)
			filtered.byHost[host] = routes
		}
		set = filtered
	}
	return renderBuiltin(set)
}

func (p *Builtin) Reload(ctx context.Context) error {
	set, err := LoadRoutes(ctx, p.db)
	if err != nil {
		p.setStatus("degraded", err)
		return err
	}
	p.routes.Store(set)
	p.setStatus("up", nil)
	return nil
}

func (p *Builtin) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

// ConfigureCertificateSource installs encrypted key decoding and the HTTP-01
// challenge handler. The challenge handler is intentionally checked before all
// reverse-proxy routing, independent of Host.
func (p *Builtin) ConfigureCertificateSource(crypto *certservice.Crypto, challenge http.Handler) {
	p.certificateMu.Lock()
	p.certCrypto = crypto
	p.challenge = challenge
	p.certificates = make(map[string]cachedCertificate)
	p.certificateMu.Unlock()
}

// TLSConfig returns the SNI-aware TLS configuration for the built-in proxy.
func (p *Builtin) TLSConfig(minVersion string) *tls.Config {
	version := uint16(tls.VersionTLS12)
	if minVersion == "1.3" {
		version = tls.VersionTLS13
	}
	return &tls.Config{
		MinVersion:     version,
		GetCertificate: p.GetCertificate,
	}
}

func (p *Builtin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if acmepkg.IsChallengePath(r.URL.Path) {
		p.certificateMu.RLock()
		challenge := p.challenge
		p.certificateMu.RUnlock()
		if challenge != nil {
			challenge.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	domain := normalizeHost(r.Host)
	route := p.routes.Load().Lookup(domain, r.URL.Path)
	if route == nil {
		p.observe(domain, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if !allowIP(r.RemoteAddr, route.AccessControl.IPWhitelist) {
		p.observe(domain, http.StatusForbidden)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if route.AccessControl.BasicAuth.Enabled {
		username, password, ok := r.BasicAuth()
		if !ok || username != route.AccessControl.BasicAuth.Username || !auth.CheckPassword(route.AccessControl.BasicAuth.PasswordHash, password) {
			p.observe(domain, http.StatusUnauthorized)
			w.Header().Set("WWW-Authenticate", `Basic realm="umpp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if route.AccessControl.RequireJWT {
		token := bearerToken(r)
		claims, err := p.auth.Parse(token, auth.TokenAccess)
		if err != nil || claims.TenantID != route.TenantID {
			p.observe(domain, http.StatusUnauthorized)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	target, err := url.Parse("http://" + route.Target)
	if err != nil {
		p.observe(domain, http.StatusBadGateway)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	originalHost := r.Host
	proto := "http"
	if r.TLS != nil {
		proto = "https"
	}
	recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = originalHost
		req.Header.Set("X-Forwarded-Host", originalHost)
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	proxy.ErrorHandler = func(response http.ResponseWriter, request *http.Request, proxyErr error) {
		p.logger.Warn("builtin proxy request failed", "host", domain, "path", request.URL.Path, "target", route.Target, "error", proxyErr)
		http.Error(response, "bad gateway", http.StatusBadGateway)
	}
	proxy.ServeHTTP(recorder, r)
	p.observe(domain, recorder.status)
}

// GetCertificate selects only the exact active, unexpired certificate for the
// requested SNI. It never falls back to another domain's certificate.
func (p *Builtin) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	domain := normalizeHost(hello.ServerName)
	if domain == "" {
		p.observeTLS("failure")
		return nil, errors.New("SNI server name is required")
	}
	if cached, ok := p.cachedCertificate(domain); ok {
		p.observeTLS("success")
		return &cached, nil
	}
	p.certificateMu.RLock()
	crypto := p.certCrypto
	p.certificateMu.RUnlock()
	if crypto == nil {
		p.observeTLS("failure")
		return nil, errors.New("certificate source is not configured")
	}
	var item models.Certificate
	now := time.Now().UTC()
	err := p.db.WithContext(hello.Context()).Where(
		"domain = ? AND status = ? AND expires_at > ?", domain, "active", now,
	).Order("expires_at DESC, renewed_at DESC, created_at DESC").First(&item).Error
	if err != nil {
		p.observeTLS("failure")
		return nil, errors.New("no active certificate for SNI " + domain)
	}
	keyPEM, err := crypto.Decrypt(item.KeyPEM)
	if err != nil {
		p.observeTLS("failure")
		return nil, errors.New("decrypt certificate for SNI " + domain)
	}
	parsed, err := tls.X509KeyPair([]byte(item.CertPEM), []byte(keyPEM))
	if err != nil {
		p.observeTLS("failure")
		return nil, errors.New("parse certificate for SNI " + domain)
	}
	if len(parsed.Certificate) == 0 {
		p.observeTLS("failure")
		return nil, errors.New("certificate chain for SNI " + domain + " is empty")
	}
	leaf, err := x509.ParseCertificate(parsed.Certificate[0])
	if err != nil || leaf.VerifyHostname(domain) != nil {
		p.observeTLS("failure")
		return nil, errors.New("certificate does not match SNI " + domain)
	}
	parsed.Leaf = leaf
	expiresAt := leaf.NotAfter
	if item.ExpiresAt != nil && item.ExpiresAt.Before(expiresAt) {
		expiresAt = *item.ExpiresAt
	}
	p.certificateMu.Lock()
	p.certificates[domain] = cachedCertificate{certificate: parsed, expiresAt: expiresAt}
	p.certificateMu.Unlock()
	p.observeTLS("success")
	return &parsed, nil
}

func (p *Builtin) cachedCertificate(domain string) (tls.Certificate, bool) {
	p.certificateMu.RLock()
	entry, ok := p.certificates[domain]
	p.certificateMu.RUnlock()
	if !ok || !time.Now().Add(30*time.Second).Before(entry.expiresAt) {
		if ok {
			p.InvalidateCertificate(domain)
		}
		return tls.Certificate{}, false
	}
	return entry.certificate, true
}

// InvalidateCertificate makes a renewed or replaced certificate immediately
// visible without restarting the TLS listener.
func (p *Builtin) InvalidateCertificate(domain string) {
	domain = normalizeHost(domain)
	p.certificateMu.Lock()
	delete(p.certificates, domain)
	p.certificateMu.Unlock()
}

func (p *Builtin) observeTLS(result string) {
	if observer, ok := p.observer.(interface{ ObserveTLSHandshake(string) }); ok {
		observer.ObserveTLSHandshake(result)
	}
}

func (p *Builtin) observe(domain string, status int) {
	if p.observer != nil {
		p.observer.ObserveProxyRequest(domain, strconv.Itoa(status))
	}
}

func (p *Builtin) setStatus(status string, err error) {
	state := State{Kind: "builtin", Status: status, UpdatedAt: time.Now().UTC()}
	if err != nil {
		state.LastError = err.Error()
	}
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
	if p.observer != nil {
		p.observer.SetProxyProviderUp("builtin", status == "up")
	}
}

func allowIP(remoteAddr string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true
	}
	host := remoteAddr
	if parsed, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = parsed
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, entry := range whitelist {
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err == nil && prefix.Contains(addr) {
				return true
			}
		} else if allowed, err := netip.ParseAddr(entry); err == nil && allowed == addr {
			return true
		}
	}
	return false
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func sortRoutes(routes []Route) {
	for i := 1; i < len(routes); i++ {
		for j := i; j > 0 && len(routes[j].Path) > len(routes[j-1].Path); j-- {
			routes[j], routes[j-1] = routes[j-1], routes[j]
		}
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
