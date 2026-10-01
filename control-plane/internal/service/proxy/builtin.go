package proxy

import (
	"bufio"
	"context"
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
)

type Builtin struct {
	db       *gorm.DB
	auth     *auth.Manager
	observer Observer
	logger   *slog.Logger
	routes   atomic.Pointer[RouteSet]
	mu       sync.RWMutex
	state    State
}

func NewBuiltin(db *gorm.DB, authManager *auth.Manager, logger *slog.Logger, observer Observer) *Builtin {
	if logger == nil {
		logger = slog.Default()
	}
	provider := &Builtin{
		db:       db,
		auth:     authManager,
		observer: observer,
		logger:   logger,
		state:    State{Kind: "builtin", Status: "unknown", UpdatedAt: time.Now().UTC()},
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

func (p *Builtin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
