package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/metrics"
	"umpp/control-plane/internal/models"
)

type contextKey int

const (
	principalKey contextKey = iota
	principalRecorderKey
)

type principalRecorder struct {
	principal Principal
	set       bool
}

type Principal struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey).(Principal)
	return principal, ok
}

func AuthRequired(manager *auth.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeUnauthorized(w)
			return
		}
		claims, err := manager.Parse(token, auth.TokenAccess)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		principal := Principal{UserID: claims.UserID, TenantID: claims.TenantID, Role: claims.Role}
		if recorder, ok := r.Context().Value(principalRecorderKey).(*principalRecorder); ok {
			recorder.principal = principal
			recorder.set = true
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal)))
	})
}

func RequireRole(roles []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || !auth.RoleAllowed(principal.Role, roles...) {
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
		ctx := context.WithValue(r.Context(), principalRecorderKey, principalState)
		started := time.Now()
		next.ServeHTTP(recorder, r.WithContext(ctx))
		if r.Method == http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			return
		}
		entry := models.AuditLog{
			ID:       uuid.New(),
			Action:   r.Method,
			Resource: r.URL.Path,
			Detail:   marshalDetail(map[string]any{"status": recorder.status, "duration_ms": time.Since(started).Milliseconds()}),
			IP:       clientIP(r),
		}
		if principalState.set {
			principal := principalState.principal
			userID := principal.UserID
			tenantID := principal.TenantID
			entry.UserID = &userID
			entry.TenantID = &tenantID
		}
		if err := db.WithContext(r.Context()).Create(&entry).Error; err != nil {
			logger.Error("failed to write audit log", "error", err, "method", r.Method, "path", r.URL.Path)
		}
	})
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

func marshalDetail(detail map[string]any) datatypes.JSON {
	value, err := json.Marshal(detail)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
