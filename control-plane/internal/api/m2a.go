package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/service"
	alertservice "neilico/control-plane/internal/service/alerts"
	acmeclient "neilico/control-plane/internal/service/cert/acme"
	"neilico/control-plane/internal/service/proxy"
)

type ProxyOptions struct {
	Enabled          bool                 `json:"enabled"`
	Kind             string               `json:"kind"`
	Listen           string               `json:"listen"`
	TLS              ProxyTLSOptions      `json:"tls"`
	ACME             acmeclient.Client    `json:"-"`
	ACMEOptions      service.ACMEOptions  `json:"-"`
	ChallengeHandler http.Handler         `json:"-"`
	Alerts           alertservice.Options `json:"-"`
	RateLimit        RateLimitOptions     `json:"-"`
	Enroll           EnrollOptions        `json:"-"`
	Downloads        DownloadsOptions     `json:"-"`
	PKI              PKIOptions           `json:"pki"`
	Dashboard        DashboardOptions     `json:"-"`
	// StreamPortMin/Max 限定「端口转发」可用的监听端口区间，必须与容器发布的端口段一致。
	StreamPortMin int `json:"-"`
	StreamPortMax int `json:"-"`
}

type DashboardOptions struct {
	Dir string
	SPA bool
}

type EnrollOptions struct {
	SigningKey string
	PublicURL  string
	// AgentImage 是接入命令里 `docker run` 用的 agent 镜像地址（必须是目标机能拉到的，
	// 具体原因见 config.Enroll.AgentImage 的注释）。
	AgentImage string
}

type DownloadsOptions struct {
	Dir string
}

type RateLimitOptions struct {
	Enabled bool
	RPS     float64
	Burst   int
}

type PKIOptions struct {
	Enabled         bool     `json:"enabled"`
	CommonName      string   `json:"common_name"`
	ServerHosts     []string `json:"server_hosts"`
	ServerCertDays  int      `json:"server_cert_days"`
	NodeCertDays    int      `json:"node_cert_days"`
	RenewBeforeDays int      `json:"renew_before_days"`
}

type ProxyTLSOptions struct {
	Enabled    bool   `json:"enabled"`
	Listen     string `json:"listen"`
	MinVersion string `json:"min_version"`
}

func (s *Server) registerM2A(mux *http.ServeMux) {
	mux.Handle("/api/v1/domains", s.authed(http.HandlerFunc(s.handleDomains)))
	mux.Handle("/api/v1/domains/", s.authed(http.HandlerFunc(s.handleDomainItem)))
	mux.Handle("/api/v1/certificates", s.authed(http.HandlerFunc(s.handleCertificates)))
	mux.Handle("/api/v1/certificates/", s.authed(http.HandlerFunc(s.handleCertificateItem)))
	mux.Handle("/api/v1/proxy-rules", s.authed(http.HandlerFunc(s.handleProxyRules)))
	mux.Handle("/api/v1/proxy-rules/", s.authed(http.HandlerFunc(s.handleProxyRuleItem)))
	mux.Handle("/api/v1/stream-rules", s.authed(http.HandlerFunc(s.handleStreamRules)))
	mux.Handle("/api/v1/stream-rules/", s.authed(http.HandlerFunc(s.handleStreamRuleItem)))
	mux.Handle("/api/v1/proxy/providers", s.authed(http.HandlerFunc(s.handleProxyProviders)))
	mux.Handle("/api/v1/proxy/render", s.authed(http.HandlerFunc(s.handleProxyRender)))
	mux.Handle("/api/v1/traffic", s.authed(http.HandlerFunc(s.handleTraffic)))
	mux.HandleFunc("/api/v1/nodes/{id}/traffic", s.handleNodeTraffic)
}

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, pageSize, err := pagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.domains.List(r.Context(), s.userScope(principal), page, pageSize)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.DomainInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.domains.Create(r.Context(), principal.TenantID, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleDomainItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/v1/domains/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "domain ID must be a UUID")
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.domains.Get(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.DomainInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.domains.Update(r.Context(), id, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		if err := s.domains.Delete(r.Context(), id, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, pageSize, err := pagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.certs.List(r.Context(), s.userScope(principal), r.URL.Query().Get("domain"), page, pageSize)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		if !canManageCertificates(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "certificate administrator role required")
			return
		}
		var input service.CertificateInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		if input.Issuer == "" {
			item, err := s.certs.Import(r.Context(), principal.TenantID, input)
			if err != nil {
				s.serviceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
			return
		}
		if input.Issuer != acmeclient.Issuer {
			writeError(w, http.StatusBadRequest, "invalid_request", "issuer must be acme or omitted for PEM import")
			return
		}
		if input.CertPEM != "" || input.KeyPEM != "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "cert_pem and key_pem must be omitted for ACME issuance")
			return
		}
		item, err := s.certs.RequestACME(r.Context(), principal.TenantID, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": item.ID, "status": item.Status})
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleCertificateItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/certificates/"), "/"), "/")
	if len(parts) == 0 || len(parts) > 2 {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "certificate ID must be a UUID")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		item, err := s.certs.Get(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case action == "" && r.Method == http.MethodDelete:
		if !canManageCertificates(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "certificate administrator role required")
			return
		}
		if err := s.certs.Delete(r.Context(), id, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		w.WriteHeader(http.StatusNoContent)
	case action == "renew" && r.Method == http.MethodPost:
		if !canManageCertificates(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "certificate administrator role required")
			return
		}
		item, err := s.certs.Renew(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": item.ID, "status": "pending"})
	case action == "revoke" && r.Method == http.MethodPost:
		if !canManageCertificates(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "certificate administrator role required")
			return
		}
		if err := s.certs.Revoke(r.Context(), id, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "revoked"})
	case action == "renew" || action == "revoke":
		s.methodNotAllowed(w, http.MethodPost)
	case action == "":
		s.methodNotAllowed(w, http.MethodGet, http.MethodDelete)
	default:
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	}
}

func (s *Server) handleProxyRules(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, pageSize, err := pagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.proxyRules.List(r.Context(), s.userScope(principal), page, pageSize)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.ProxyRuleInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.proxyRules.Create(r.Context(), principal.TenantID, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpTenantNodes(r.Context(), item.TenantID, "proxy rule added"); err != nil {
			s.serviceError(w, err)
			return
		}
		if _, err := s.configs.BumpForProxy(r.Context(), item.ID, "proxy rule added"); err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProxyRuleItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/v1/proxy-rules/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "proxy rule ID must be a UUID")
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.proxyRules.Get(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.ProxyRuleInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.proxyRules.Update(r.Context(), id, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpTenantNodes(r.Context(), item.TenantID, "proxy rule updated"); err != nil {
			s.serviceError(w, err)
			return
		}
		if _, err := s.configs.BumpForProxy(r.Context(), item.ID, "proxy rule updated"); err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		item, err := s.proxyRules.Get(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if _, err := s.configs.BumpForProxy(r.Context(), item.ID, "proxy rule removed"); err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.proxyRules.Delete(r.Context(), id, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpTenantNodes(r.Context(), item.TenantID, "proxy rule removed"); err != nil {
			s.serviceError(w, err)
			return
		}
		s.reloadProxy(r)
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleProxyProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	kind := s.proxy.Kind()
	if s.proxyOpts.Kind != "" {
		kind = s.proxyOpts.Kind
	}
	state := proxy.State{Kind: kind, Status: "unknown"}
	if provider, ok := s.proxy.(proxy.StateProvider); ok {
		state = provider.State()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":         s.proxyOpts.Enabled,
		"configured_kind": kind,
		"listen":          s.proxyOpts.Listen,
		"state":           state,
	})
}

func (s *Server) handleProxyRender(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "tenant administrator role required")
		return
	}
	data, err := s.proxy.Render(r.Context(), principal.TenantID)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-NEILICO-Proxy-Kind", s.proxy.Kind())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	page, pageSize, err := pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	filter := service.TrafficFilter{Page: page, Size: pageSize}
	if raw := r.URL.Query().Get("node_id"); raw != "" {
		nodeID, parseErr := parseID(raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "node_id must be a UUID")
			return
		}
		filter.NodeID = &nodeID
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "from must be an RFC 3339 timestamp")
			return
		}
		filter.From = &value
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "to must be an RFC 3339 timestamp")
			return
		}
		filter.To = &value
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		writeError(w, http.StatusBadRequest, "invalid_request", "from must not be after to")
		return
	}
	result, err := s.traffic.List(r.Context(), s.userScope(principal), filter)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleNodeTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "node ID must be a UUID")
		return
	}
	var input []service.TrafficInput
	if !s.decodeRequestArray(w, r, &input) {
		return
	}
	accepted, err := s.traffic.Record(r.Context(), id, bearerToken(r), input)
	if errors.Is(err, service.ErrForbidden) {
		writeError(w, http.StatusUnauthorized, "invalid_agent_token", "valid agent token required")
		return
	}
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"accepted": accepted})
}

func (s *Server) reloadProxy(r *http.Request) {
	if s.proxy == nil {
		return
	}
	if err := s.proxy.Reload(r.Context()); err != nil {
		s.logger.Warn("proxy provider reload failed", "kind", s.proxy.Kind(), "error", err)
	}
}

func (s *Server) decodeRequestArray(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	return true
}

func canManageProxy(ctx context.Context, principal middleware.Principal) bool {
	return roleAllowed(ctx, principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps)
}

func canManageCertificates(ctx context.Context, principal middleware.Principal) bool {
	return roleAllowed(ctx, principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin)
}
