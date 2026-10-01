package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/middleware"
	alertservice "umpp/control-plane/internal/service/alerts"
)

func (s *Server) registerAlerts(mux *http.ServeMux) {
	mux.Handle("/api/v1/alerts", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleAlerts)))
	mux.Handle("/api/v1/alerts/rules", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleAlertRules)))
	mux.Handle("/api/v1/alerts/summary", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleAlertSummary)))
	mux.Handle("/api/v1/alerts/evaluate", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleAlertEvaluate)))
	mux.Handle("/api/v1/alerts/", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleAlertItem)))
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	scope, ok := s.alertTenantScope(w, r, principal)
	if !ok {
		return
	}
	page, pageSize, err := pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	filter := alertservice.ListFilter{
		TenantID: scope, Page: page, PageSize: pageSize,
		State:      strings.TrimSpace(r.URL.Query().Get("state")),
		Severity:   strings.TrimSpace(r.URL.Query().Get("severity")),
		Rule:       strings.TrimSpace(r.URL.Query().Get("rule")),
		TargetType: strings.TrimSpace(r.URL.Query().Get("target_type")),
	}
	if filter.State != "" && filter.State != alertservice.StateFiring && filter.State != alertservice.StateResolved {
		writeError(w, http.StatusBadRequest, "invalid_request", "state must be firing or resolved")
		return
	}
	if filter.Severity != "" && !auth.RoleAllowed(filter.Severity, alertservice.SeverityInfo, alertservice.SeverityWarning, alertservice.SeverityCritical) {
		writeError(w, http.StatusBadRequest, "invalid_request", "severity must be info, warning, or critical")
		return
	}
	if filter.Rule != "" && !auth.RoleAllowed(filter.Rule, s.alertRules()...) {
		writeError(w, http.StatusBadRequest, "invalid_request", "unknown alert rule")
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("target_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "target_id must be a UUID")
			return
		}
		filter.TargetID = &id
	}
	result, err := s.alertEngine.List(r.Context(), filter)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAlertRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	items := s.alertEngine.Rules()
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items),
		"evaluation_interval_seconds": s.alertEngine.Options().EvaluationInterval.Seconds(),
	})
}

func (s *Server) handleAlertSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	scope, ok := s.alertTenantScope(w, r, principal)
	if !ok {
		return
	}
	summary, err := s.alertEngine.Summary(r.Context(), scope)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if summary.ByRule == nil {
		summary.ByRule = map[string]int64{}
	}
	for _, rule := range s.alertRules() {
		if _, ok := summary.ByRule[rule]; !ok {
			summary.ByRule[rule] = 0
		}
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleAlertEvaluate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if !auth.RoleAllowed(principal.Role, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps) {
		writeError(w, http.StatusForbidden, "forbidden", "admin or ops role required")
		return
	}
	scope, ok := s.alertTenantScope(w, r, principal)
	if !ok {
		return
	}
	tenantID := principal.TenantID
	if scope != nil {
		tenantID = *scope
	}
	results := s.alertEngine.Evaluate(r.Context(), tenantID)
	alerts := make([]alertservice.Alert, 0, len(results))
	insufficient := make([]alertservice.Alert, 0)
	for _, result := range results {
		if result.DataStatus == alertservice.DataStatusInsufficientData {
			insufficient = append(insufficient, result)
		} else {
			alerts = append(alerts, result)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": alerts, "total": len(alerts),
		"insufficient_data": insufficient,
		"evaluated_at":      resultsTimestamp(results),
	})
}

func (s *Server) handleAlertItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "alert ID must be a UUID")
		return
	}
	var scope *uuid.UUID
	if principal.Role != auth.RolePlatformAdmin {
		scope = &principal.TenantID
	}
	alert, events, err := s.alertEngine.Get(r.Context(), id, scope)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "alert not found")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert": alert, "events": events})
}

func (s *Server) alertTenantScope(w http.ResponseWriter, r *http.Request, principal middleware.Principal) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" {
		if principal.Role == auth.RolePlatformAdmin {
			return nil, true
		}
		return &principal.TenantID, true
	}
	if principal.Role != auth.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "only platform_admin may select tenant_id")
		return nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "tenant_id must be a UUID")
		return nil, false
	}
	return &id, true
}

func (s *Server) alertRules() []string {
	return []string{
		alertservice.RuleNodeOffline,
		alertservice.RuleCertificateExpiring,
		alertservice.RuleP2PSuccessRateLow,
		alertservice.RuleRelayTrafficSpike,
		alertservice.RuleConfigDispatchFailed,
	}
}

func resultsTimestamp(results []alertservice.Alert) any {
	if len(results) == 0 {
		return nil
	}
	return results[0].EvaluatedAt
}
