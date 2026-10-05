package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/models"
	certservice "neilico/control-plane/internal/service/cert"
	acmepkg "neilico/control-plane/internal/service/cert/acme"
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

	// transport 是 http 上游共享的连接池；https 上游按规则单独建（TLS 参数不同），
	// 两者都必须复用，否则每请求重新建连会让反代吞吐掉一个数量级。
	transport  *http.Transport
	proxyMu    sync.RWMutex
	proxies    map[uuid.UUID]cachedProxy
	proxyEpoch uint64
}

// cachedProxy 是按规则缓存的 ReverseProxy。路由重载时 epoch 变化即失效。
type cachedProxy struct {
	proxy *httputil.ReverseProxy
	epoch uint64
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
		transport:    newUpstreamTransport(),
		proxies:      make(map[uuid.UUID]cachedProxy),
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
	p.invalidateProxies()
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
			w.Header().Set("WWW-Authenticate", `Basic realm="neilico"`)
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
	scheme := route.UpstreamScheme
	if scheme == "" {
		scheme = "http"
	}
	if _, err := url.Parse(scheme + "://" + route.Target); err != nil {
		p.observe(domain, http.StatusBadGateway)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
	proxy, proxyErr := p.reverseProxyFor(*route)
	if proxyErr != nil {
		p.logger.Error("https upstream transport configuration failed", "route_id", route.RuleID, "error", proxyErr)
		p.observe(domain, http.StatusBadGateway)
		http.Error(w, "bad gateway: https upstream transport configuration failed", http.StatusBadGateway)
		return
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
	if observer, ok := p.observer.(interface{ ObserveTLSHandshake(string, string) }); ok {
		observer.ObserveTLSHandshake(result, "proxy")
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

// reverseProxyFor 返回按规则缓存的 ReverseProxy。复用代理能保住上游连接池；
// 每请求新建会把 http.DefaultTransport 的 MaxIdleConnsPerHost(=2) 暴露出来，
// 并发下退化成每次请求重新建连（实测：吞吐差 4 倍、p99 差一个数量级）。
func (p *Builtin) reverseProxyFor(route Route) (*httputil.ReverseProxy, error) {
	p.proxyMu.RLock()
	entry, ok := p.proxies[route.RuleID]
	epoch := p.proxyEpoch
	p.proxyMu.RUnlock()
	if ok && entry.epoch == epoch {
		return entry.proxy, nil
	}
	proxy, err := p.buildProxy(route)
	if err != nil {
		return nil, err
	}
	p.proxyMu.Lock()
	if p.proxies == nil {
		p.proxies = make(map[uuid.UUID]cachedProxy)
	}
	p.proxies[route.RuleID] = cachedProxy{proxy: proxy, epoch: p.proxyEpoch}
	p.proxyMu.Unlock()
	return proxy, nil
}

// invalidateProxies 在路由重载后调用：规则的目标/协议变了，代理与其连接池都必须重建。
func (p *Builtin) invalidateProxies() {
	p.proxyMu.Lock()
	p.proxies = make(map[uuid.UUID]cachedProxy)
	p.proxyEpoch++
	p.proxyMu.Unlock()
}

func (p *Builtin) buildProxy(route Route) (*httputil.ReverseProxy, error) {
	scheme := route.UpstreamScheme
	if scheme == "" {
		scheme = "http"
	}
	target, err := url.Parse(scheme + "://" + route.Target)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if scheme == "https" {
		transport, transportErr := upstreamTransport(route, target)
		if transportErr != nil {
			return nil, transportErr
		}
		proxy.Transport = transport
	} else {
		transport := p.transport
		if transport == nil {
			transport = sharedUpstreamTransport
		}
		proxy.Transport = transport
	}
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		// 必须从 req 自身取这两个值：代理实例是被复用的，不能闭包捕获请求级数据。
		originalHost := req.Host
		proto := "http"
		if req.TLS != nil {
			proto = "https"
		}
		director(req)
		req.Host = originalHost
		req.Header.Set("X-Forwarded-Host", originalHost)
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	proxy.ErrorHandler = func(response http.ResponseWriter, request *http.Request, proxyErr error) {
		p.logger.Warn("builtin proxy request failed", "host", normalizeHost(request.Host), "path", request.URL.Path, "target", route.Target, "error", proxyErr)
		http.Error(response, "bad gateway", http.StatusBadGateway)
	}
	return proxy, nil
}

// sharedUpstreamTransport 兜底给未经 NewBuiltin 构造的实例用（保持无写入、无竞态）。
var sharedUpstreamTransport = newUpstreamTransport()

// newUpstreamTransport 是 http 上游共享的连接池。Go 默认的 MaxIdleConnsPerHost=2
// 在并发场景下会让请求几乎每次都重新建连。
func newUpstreamTransport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.MaxIdleConns = 4096
	base.MaxIdleConnsPerHost = 512
	base.IdleConnTimeout = 90 * time.Second
	base.ExpectContinueTimeout = time.Second
	base.ForceAttemptHTTP2 = true
	return base
}

func upstreamTransport(route Route, target *url.URL) (*http.Transport, error) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         target.Hostname(),
		InsecureSkipVerify: route.UpstreamInsecureSkipVerify,
	}
	if route.UpstreamCAFile != "" {
		pemData, err := os.ReadFile(route.UpstreamCAFile)
		if err != nil {
			return nil, fmt.Errorf("read upstream CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, errors.New("upstream CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	base.TLSClientConfig = tlsConfig
	return base, nil
}
