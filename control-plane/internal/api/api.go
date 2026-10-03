package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/dashboard"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
	alertservice "neilico/control-plane/internal/service/alerts"
	"neilico/control-plane/internal/service/cert"
	acmeclient "neilico/control-plane/internal/service/cert/acme"
	configservice "neilico/control-plane/internal/service/config"
	"neilico/control-plane/internal/service/pki"
	"neilico/control-plane/internal/service/proxy"
)

const maxRequestBody = 1 << 20

type Server struct {
	db            *gorm.DB
	auth          *auth.Manager
	tenants       *service.TenantService
	users         *service.UserService
	nodes         *service.NodeService
	domains       *service.DomainService
	certs         *service.CertificateService
	proxyRules    *service.ProxyRuleService
	auditLogs     *service.AuditLogService
	relays        *service.RelayServerService
	observability *service.ObservabilityService
	traffic       *service.TrafficService
	networks      *service.NetworkService
	configs       *configservice.Manager
	pki           *pki.Service
	apiTokens     *service.APITokenService
	enrollTokens  *service.NodeEnrollTokenService
	enrollNodes   *service.NodeEnrollService
	tokenUsage    *middleware.APITokenUsageTracker
	rateLimiter   *middleware.Limiter
	alertEngine   *alertservice.Engine
	metrics       *metrics.Metrics
	logger        *slog.Logger
	version       string
	proxy         proxy.Provider
	proxyOpts     ProxyOptions
	downloadsDir  string
	enrollURL     string
	startedAt     time.Time
}

// Handler couples the middleware-wrapped HTTP handler with lifecycle controls
// used by cmd/api.
type Handler struct {
	http.Handler
	server *Server
}

func (h *Handler) PKI() *pki.Service { return h.server.pki }

func (h *Handler) StartCertificateLifecycle(ctx context.Context) {
	h.server.certs.Start(ctx)
}

func (h *Handler) StartAlertEvaluation(ctx context.Context) {
	h.server.alertEngine.Run(ctx)
}

func New(
	db *gorm.DB,
	authManager *auth.Manager,
	nodeService *service.NodeService,
	promMetrics *metrics.Metrics,
	logger *slog.Logger,
	version string,
) *Handler {
	return NewWithProxy(db, authManager, nodeService, promMetrics, logger, version, nil, ProxyOptions{
		Enabled: true,
		Kind:    "builtin",
		Listen:  ":8081",
	})
}

func NewWithProxy(
	db *gorm.DB,
	authManager *auth.Manager,
	nodeService *service.NodeService,
	promMetrics *metrics.Metrics,
	logger *slog.Logger,
	version string,
	proxyProvider proxy.Provider,
	opts ProxyOptions,
) *Handler {
	if proxyProvider == nil {
		proxyProvider = proxy.NewBuiltin(db, authManager, logger, promMetrics)
	}
	certificateCrypto, err := cert.NewCryptoFromKey(authManager.CertificateEncryptionKey())
	if err != nil {
		panic(err)
	}
	nodeService.ConfigureKeyCrypto(certificateCrypto)
	if opts.ACME == nil {
		opts.ACME = acmeclient.New(acmeclient.Config{
			Enabled:   false,
			Challenge: acmeclient.ChallengeHTTP01,
		}, nil)
	}
	var alertNotifier alertservice.Notifier = alertservice.NewLogNotifier(logger)
	if opts.Alerts.WebhookURL != "" {
		webhookNotifier, err := alertservice.NewWebhookNotifier(
			opts.Alerts.WebhookURL, opts.Alerts.WebhookTimeout, opts.Alerts.WebhookRetries, logger,
		)
		if err != nil {
			panic(err)
		}
		alertNotifier = alertservice.NewMultiNotifier(alertNotifier, webhookNotifier)
	}
	alertEngine := alertservice.NewEngine(db, opts.Alerts, alertNotifier, promMetrics, logger)
	configManager := configservice.New(db, certificateCrypto, promMetrics)
	pkiService := pki.New(db, certificateCrypto, pki.Options{
		Enabled:         opts.PKI.Enabled,
		CommonName:      opts.PKI.CommonName,
		ServerHosts:     opts.PKI.ServerHosts,
		ServerCertDays:  opts.PKI.ServerCertDays,
		NodeCertDays:    opts.PKI.NodeCertDays,
		RenewBeforeDays: opts.PKI.RenewBeforeDays,
	}, promMetrics)
	certificateService := service.NewCertificateService(db, certificateCrypto)
	var certificateInvalidator service.CertificateCacheInvalidator
	if builtin, ok := proxyProvider.(*proxy.Builtin); ok {
		builtin.ConfigureCertificateSource(certificateCrypto, opts.ChallengeHandler)
		certificateInvalidator = builtin
	}
	certificateService.ConfigureACME(opts.ACME, opts.ACMEOptions, promMetrics, configManager, certificateInvalidator, logger)
	server := &Server{
		db:            db,
		auth:          authManager,
		tenants:       service.NewTenantService(db),
		users:         service.NewUserService(db),
		nodes:         nodeService,
		domains:       service.NewDomainService(db),
		certs:         certificateService,
		proxyRules:    service.NewProxyRuleService(db),
		auditLogs:     service.NewAuditLogService(db),
		relays:        service.NewRelayServerService(db),
		observability: service.NewObservabilityService(db),
		traffic:       service.NewTrafficService(db),
		networks:      service.NewNetworkService(db, certificateCrypto),
		configs:       configManager,
		pki:           pkiService,
		apiTokens:     service.NewAPITokenService(db),
		enrollTokens:  service.NewNodeEnrollTokenService(db, enrollSigningKey(authManager, opts.Enroll.SigningKey)),
		enrollNodes:   service.NewNodeEnrollService(db, nodeService, enrollSigningKey(authManager, opts.Enroll.SigningKey)),
		downloadsDir:  opts.Downloads.Dir,
		enrollURL:     strings.TrimRight(strings.TrimSpace(opts.Enroll.PublicURL), "/"),
		alertEngine:   alertEngine,
		metrics:       promMetrics,
		logger:        logger,
		version:       version,
		proxy:         proxyProvider,
		proxyOpts:     opts,
		startedAt:     time.Now().UTC(),
	}
	server.tokenUsage = middleware.NewAPITokenUsageTracker(db, logger, middleware.DefaultAPITokenUsageInterval)
	if opts.RateLimit.Enabled && opts.RateLimit.RPS > 0 && opts.RateLimit.Burst > 0 {
		server.rateLimiter = middleware.NewLimiter(opts.RateLimit.RPS, opts.RateLimit.Burst)
	}
	mux := http.NewServeMux()
	server.registerM2A(mux)
	server.registerM2B(mux)
	server.registerM4B(mux)
	server.registerEnroll(mux)
	server.registerPublicDownloads(mux)

	mux.HandleFunc("/healthz", server.handleHealth)
	mux.Handle("/metrics", promMetrics.Handler())
	mux.HandleFunc("/api/v1/pki/ca", server.handlePKICA)
	mux.Handle("/api/v1/pki/ca/rotate", server.authed(http.HandlerFunc(server.handlePKICARotate)))
	mux.Handle("/api/v1/nodes/{id}/mtls", server.authed(http.HandlerFunc(server.handleNodeMTLS)))
	mux.HandleFunc("/api/v1/auth/login", server.handleLogin)
	mux.HandleFunc("/api/v1/auth/refresh", server.handleRefresh)

	server.registerAlerts(mux)
	server.registerAPITokens(mux)
	mux.Handle("/api/v1/tenants", server.requireRole(server.handleTenants, auth.RolePlatformAdmin))
	mux.Handle("/api/v1/tenants/", server.requireRole(server.handleTenantItem, auth.RolePlatformAdmin))
	mux.Handle("/api/v1/users", server.authed(http.HandlerFunc(server.handleUsers)))
	mux.Handle("/api/v1/users/", server.authed(http.HandlerFunc(server.handleUserItem)))

	mux.Handle("/api/v1/nodes/register", server.requireRole(server.handleNodeRegister, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps))
	mux.Handle("/api/v1/nodes", server.authed(http.HandlerFunc(server.handleNodes)))
	mux.HandleFunc("/api/v1/nodes/{id}/heartbeat", server.handleNodeHeartbeatRoute)
	mux.Handle("/api/v1/nodes/{id}", server.authed(http.HandlerFunc(server.handleNodeItem)))
	mux.HandleFunc("/api/v1/", server.handleAPIFallback)

	if opts.Dashboard.Dir != "" {
		challengeHandler := opts.ChallengeHandler
		if challengeHandler == nil {
			challengeHandler = http.NotFoundHandler()
		}
		mux.Handle("/api/", http.NotFoundHandler())
		mux.Handle("/.well-known/acme-challenge/", challengeHandler)
		mux.Handle("/", dashboard.New(dashboard.Options{Dir: opts.Dashboard.Dir, SPA: opts.Dashboard.SPA}))
	}

	var handler http.Handler = mux
	if opts.Dashboard.Dir != "" {
		handler = dashboard.Guard(handler)
	}
	handler = middleware.Audit(db, logger, handler)
	handler = middleware.Metrics(promMetrics, handler)
	return &Handler{Handler: handler, server: server}
}

func (s *Server) authed(next http.Handler) http.Handler {
	return middleware.AuthRequired(s.auth, s.db, s.tokenUsage, middleware.RateLimit(s.rateLimiter, next))
}

func roleAllowed(ctx context.Context, principal middleware.Principal, roles ...string) bool {
	if principal.AuthMethod == auth.AuthMethodAPIToken {
		return middleware.ScopeAuthorized(ctx)
	}
	return auth.RoleAllowed(principal.Role, roles...)
}

func (s *Server) requireRole(next http.HandlerFunc, roles ...string) http.Handler {
	return s.authed(middleware.RequireRole(roles, next))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	status := http.StatusOK
	dbState := "up"
	if err := s.db.WithContext(r.Context()).Exec("SELECT 1").Error; err != nil {
		status = http.StatusServiceUnavailable
		dbState = "down"
	}
	writeJSON(w, status, map[string]any{
		"status":  map[bool]string{true: "ok", false: "unavailable"}[status == http.StatusOK],
		"db":      dbState,
		"version": s.version,
		"uptime":  time.Since(s.startedAt).Seconds(),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return
	}
	user, err := s.users.ActiveByEmail(r.Context(), input.Email)
	if err != nil || !auth.CheckPassword(user.PasswordHash, input.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	response, err := s.tokenResponse(user)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return
	}
	if input.RefreshToken == "" {
		input.RefreshToken = bearerToken(r)
	}
	claims, err := s.auth.Parse(input.RefreshToken, auth.TokenRefresh)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "valid refresh token required")
		return
	}
	user, err := s.users.Get(r.Context(), claims.UserID, &claims.TenantID)
	if err != nil || user.Status != "active" {
		writeError(w, http.StatusUnauthorized, "invalid_token", "valid refresh token required")
		return
	}
	response, tokenErr := s.tokenResponse(user)
	if tokenErr != nil {
		s.internalError(w, tokenErr)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) tokenResponse(user models.User) (map[string]any, error) {
	access, err := s.auth.IssueAccess(user)
	if err != nil {
		return nil, err
	}
	refresh, err := s.auth.IssueRefresh(user)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"token":         access,
		"refresh_token": refresh,
		"user": map[string]any{
			"id":        user.ID,
			"email":     user.Email,
			"role":      user.Role,
			"tenant_id": user.TenantID,
		},
	}, nil
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tenants, err := s.tenants.List(r.Context())
		if err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": tenants, "total": len(tenants)})
	case http.MethodPost:
		var input service.TenantInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		tenant, err := s.tenants.Create(r.Context(), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, tenant)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTenantItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/v1/tenants/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "tenant ID must be a UUID")
		return
	}
	switch r.Method {
	case http.MethodGet:
		tenant, err := s.tenants.Get(r.Context(), id)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tenant)
	case http.MethodPut:
		var input service.TenantInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		tenant, err := s.tenants.Update(r.Context(), id, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tenant)
	case http.MethodDelete:
		if err := s.tenants.Delete(r.Context(), id); err != nil {
			s.serviceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, pageSize, err := pagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
			return
		}
		scope := s.userScope(principal)
		users, total, err := s.users.List(r.Context(), scope, page, pageSize)
		if err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": users, "total": total, "page": page, "page_size": pageSize})
	case http.MethodPost:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.UserInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		if !canAssignRole(principal.Role, input.Role) {
			writeError(w, http.StatusForbidden, "forbidden", "cannot assign platform_admin role")
			return
		}
		user, err := s.users.Create(r.Context(), input, principal.TenantID, principal.Role == auth.RolePlatformAdmin)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, user)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleUserItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/v1/users/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "user ID must be a UUID")
		return
	}
	scope := s.userScope(principal)
	switch r.Method {
	case http.MethodGet:
		user, err := s.users.Get(r.Context(), id, scope)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	case http.MethodPut:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.UserInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		if !canAssignRole(principal.Role, input.Role) {
			writeError(w, http.StatusForbidden, "forbidden", "cannot assign platform_admin role")
			return
		}
		user, err := s.users.Update(r.Context(), id, input, scope)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	case http.MethodDelete:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		if err := s.users.Delete(r.Context(), id, scope); err != nil {
			s.serviceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleNodeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	var input service.NodeRegisterInput
	if !s.decodeRequest(w, r, &input) {
		return
	}
	node, err := s.nodes.Register(r.Context(), principal.TenantID, input)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	filter := service.NodeListFilter{
		Status:   r.URL.Query().Get("status"),
		Tag:      r.URL.Query().Get("tag"),
		Page:     page,
		PageSize: pageSize,
	}
	var tenantID *uuid.UUID
	if principal.Role != auth.RolePlatformAdmin {
		tenantID = &principal.TenantID
	}
	list, err := s.nodes.List(r.Context(), tenantID, filter)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNodeHeartbeatRoute(w http.ResponseWriter, r *http.Request) {
	s.handleHeartbeat(w, r, r.PathValue("id"))
}

func (s *Server) handleNodeItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "node ID must be a UUID")
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	var tenantID *uuid.UUID
	if principal.Role != auth.RolePlatformAdmin {
		tenantID = &principal.TenantID
	}
	switch r.Method {
	case http.MethodGet:
		node, err := s.nodes.Get(r.Context(), id, tenantID)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	case http.MethodDelete:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		if err := s.nodes.Delete(r.Context(), id, tenantID); err != nil {
			s.serviceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodDelete)
	}
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "node ID must be a UUID")
		return
	}
	var input service.HeartbeatInput
	if !s.decodeRequest(w, r, &input) {
		return
	}
	node, err := s.nodes.Heartbeat(r.Context(), id, bearerToken(r), input)
	if errors.Is(err, service.ErrForbidden) {
		writeError(w, http.StatusUnauthorized, "invalid_agent_token", "valid agent token required")
		return
	}
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                     true,
		"next_heartbeat_seconds": 30,
		"server_time":            time.Now().UTC(),
		"node":                   node,
	})
}

func (s *Server) handleAPIFallback(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func canAssignRole(actorRole, assignedRole string) bool {
	return actorRole == auth.RolePlatformAdmin || assignedRole != auth.RolePlatformAdmin
}

func (s *Server) userScope(principal middleware.Principal) *uuid.UUID {
	if principal.Role == auth.RolePlatformAdmin {
		return nil
	}
	return &principal.TenantID
}

func (s *Server) decodeRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := decodeJSON(w, r, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	return true
}

// isForeignKeyViolation 判断错误是否来自外键约束冲突（即「还有别的行引用它」）。
// 同时匹配 PostgreSQL 的 SQLSTATE 23503、其错误文案，以及 SQLite 的同类文案，
// 以免为了拿驱动错误类型而引入额外依赖。
func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "SQLSTATE 23503") ||
		strings.Contains(message, "violates foreign key constraint") ||
		strings.Contains(message, "FOREIGN KEY constraint failed")
}

func (s *Server) serviceError(w http.ResponseWriter, err error) {
	// 外键冲突（PostgreSQL SQLSTATE 23503）：说明还有引用没清干净。
	// 这是「有依赖、无法删除」而非服务端故障，回 409 并给出可操作信息，别兜成 500。
	if isForeignKeyViolation(err) {
		writeError(w, http.StatusConflict, "conflict", "resource is still referenced by other records")
		return
	}
	if errors.Is(err, service.ErrOrderInFlight) {
		writeError(w, http.StatusConflict, "order_in_flight", err.Error())
		return
	}
	var acmeErr *acmeclient.Error
	if errors.As(err, &acmeErr) {
		status := http.StatusUnprocessableEntity
		switch acmeErr.Code {
		case "acme_disabled", "acme_tos_not_accepted":
			status = http.StatusConflict
		case "acme_revoke_not_implemented":
			status = http.StatusNotImplemented
		}
		writeError(w, status, acmeErr.Code, acmeErr.Error())
		return
	}
	if errors.Is(err, acmeclient.ErrNotImplemented) {
		writeError(w, http.StatusNotImplemented, "not_implemented", err.Error())
		return
	}
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, service.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "access denied")
	case errors.Is(err, service.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "resource conflict")
	case errors.Is(err, service.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, proxy.ErrUnprocessable):
		writeError(w, http.StatusUnprocessableEntity, "unprocessable_entity", err.Error())
	default:
		s.internalError(w, err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func (s *Server) methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func parseID(raw string) (uuid.UUID, error) {
	return uuid.Parse(raw)
}

func pagination(r *http.Request) (int, int, error) {
	page := 1
	pageSize := 20
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			return 0, 0, errors.New("page must be a positive integer")
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return 0, 0, errors.New("page_size must be between 1 and 100")
		}
	}
	return page, pageSize, nil
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetail(w, status, code, message, nil)
}

func writeErrorDetail(w http.ResponseWriter, status int, code, message string, detail map[string]any) {
	payload := map[string]any{"code": code, "message": message}
	if detail != nil {
		payload["detail"] = detail
	}
	writeJSON(w, status, map[string]any{"error": payload})
}
