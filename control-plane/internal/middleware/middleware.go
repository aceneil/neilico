package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/models"
)

type contextKey int

const (
	principalKey contextKey = iota
	principalRecorderKey
	auditStateKey
	scopeAuthorizedKey
)

type principalRecorder struct {
	principal Principal
	set       bool
}

type auditState struct {
	action   string
	resource string
	detail   map[string]any
}

type Principal struct {
	UserID      uuid.UUID
	TenantID    uuid.UUID
	Role        string
	Scopes      []string
	AuthMethod  string
	APITokenID  uuid.UUID
	TokenPrefix string
}

type AuthContext = Principal

func ScopeAuthorized(ctx context.Context) bool {
	authorized, _ := ctx.Value(scopeAuthorizedKey).(bool)
	return authorized
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey).(Principal)
	return principal, ok
}

func AuthRequired(manager *auth.Manager, db *gorm.DB, usage *APITokenUsageTracker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeUnauthorized(w)
			return
		}
		var principal Principal
		if auth.IsAPIToken(token) {
			authenticated, code, err := authenticateAPIToken(db, usage, token, clientIP(r))
			if err != nil {
				writeError(w, http.StatusUnauthorized, code, "valid API token required")
				return
			}
			principal = authenticated
		} else {
			claims, err := manager.Parse(token, auth.TokenAccess)
			if err != nil {
				writeUnauthorized(w)
				return
			}
			principal = Principal{
				UserID: claims.UserID, TenantID: claims.TenantID, Role: claims.Role,
				Scopes: auth.ScopesForRole(claims.Role), AuthMethod: auth.AuthMethodJWT,
			}
		}
		if recorder, ok := r.Context().Value(principalRecorderKey).(*principalRecorder); ok {
			recorder.principal = principal
			recorder.set = true
		}
		ctx := context.WithValue(r.Context(), principalKey, principal)
		RequireScopeForRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})).ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticateAPIToken(db *gorm.DB, usage *APITokenUsageTracker, plain, ip string) (Principal, string, error) {
	var item models.APIToken
	err := db.Where("token_hash = ?", auth.HashAPIToken(plain)).First(&item).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Principal{}, "invalid_token", err
		}
		return Principal{}, "invalid_token", err
	}
	if item.RevokedAt != nil {
		return Principal{}, "token_revoked", errors.New("API token revoked")
	}
	if item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now().UTC()) {
		return Principal{}, "token_expired", errors.New("API token expired")
	}
	var tenant models.Tenant
	if err := db.First(&tenant, "id = ?", item.TenantID).Error; err != nil {
		return Principal{}, "invalid_token", err
	}
	userID := uuid.Nil
	if item.UserID != nil {
		var user models.User
		if err := db.First(&user, "id = ? AND tenant_id = ?", *item.UserID, item.TenantID).Error; err != nil || user.Status != "active" {
			return Principal{}, "invalid_token", errors.New("API token user invalid")
		}
		userID = user.ID
	}
	usage.Track(item.ID, ip)
	return Principal{
		UserID: userID, TenantID: item.TenantID,
		Scopes:     append([]string(nil), item.Scopes...),
		AuthMethod: auth.AuthMethodAPIToken, APITokenID: item.ID, TokenPrefix: item.TokenPrefix,
	}, "", nil
}

func RequireRole(roles []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		if principal.AuthMethod == auth.AuthMethodAPIToken {
			if !ScopeAuthorized(r.Context()) {
				writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
				return
			}
		} else if !auth.RoleAllowed(principal.Role, roles...) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Metrics(m *metrics.Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		m.ObserveHTTP(r.Method, r.URL.Path, recorder.status)
	})
}

func Audit(db *gorm.DB, logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		principalState := &principalRecorder{}
		audit := &auditState{action: r.Method, resource: r.URL.Path, detail: map[string]any{}}
		ctx := context.WithValue(r.Context(), principalRecorderKey, principalState)
		ctx = context.WithValue(ctx, auditStateKey, audit)
		started := time.Now()
		next.ServeHTTP(recorder, r.WithContext(ctx))
		if r.Method == http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			return
		}
		detail := audit.detail
		if detail == nil {
			detail = map[string]any{}
		}
		detail["status"] = recorder.status
		detail["duration_ms"] = time.Since(started).Milliseconds()
		entry := models.AuditLog{
			ID:       uuid.New(),
			Action:   audit.action,
			Resource: audit.resource,
			Detail:   marshalDetail(detail),
			IP:       clientIP(r),
		}
		if principalState.set {
			principal := principalState.principal
			userID := principal.UserID
			tenantID := principal.TenantID
			if userID != uuid.Nil {
				entry.UserID = &userID
			}
			entry.TenantID = &tenantID
		}
		if err := db.WithContext(r.Context()).Create(&entry).Error; err != nil {
			logger.Error("failed to write audit log", "error", err, "method", r.Method, "path", r.URL.Path)
		}
	})
}

func SetAuditAction(ctx context.Context, action, resource string, detail map[string]any) {
	state, ok := ctx.Value(auditStateKey).(*auditState)
	if !ok {
		return
	}
	if action != "" {
		state.action = action
	}
	if resource != "" {
		state.resource = resource
	}
	if detail != nil {
		state.detail = detail
	}
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

var embeddedAPIToken = regexp.MustCompile(`neilico_[A-Za-z0-9_-]{20,}`)

func sanitizeDetail(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if strings.Contains(strings.ToLower(key), "token") {
				if text, ok := child.(string); ok {
					result[key] = embeddedAPIToken.ReplaceAllString(text, auth.RedactAPIToken(embeddedAPIToken.FindString(text)))
					continue
				}
			}
			result[key] = sanitizeDetail(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = sanitizeDetail(child)
		}
		return result
	case string:
		return embeddedAPIToken.ReplaceAllStringFunc(typed, auth.RedactAPIToken)
	default:
		return value
	}
}

func marshalDetail(detail map[string]any) datatypes.JSON {
	value, err := json.Marshal(sanitizeDetail(detail))
	if err != nil {
		return datatypes.JSON(`{"status":500}`)
	}
	return value
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "0.0.0.0"
}

func writeUnauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetail(w, status, code, message, nil)
}

func writeErrorDetail(w http.ResponseWriter, status int, code, message string, detail map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]any{"code": code, "message": message}
	if detail != nil {
		payload["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": payload})
}
