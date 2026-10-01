package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/middleware"
	"umpp/control-plane/internal/service"
)

func (s *Server) registerM4B(mux *http.ServeMux) {
	mux.Handle("/api/v1/audit-logs", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleAuditLogs)))
	mux.Handle("/api/v1/nodes/{id}/metrics", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleNodeMetrics)))
	mux.Handle("/api/v1/relay-servers", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleRelayServers)))
	mux.Handle("/api/v1/relay-servers/{id}", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleRelayServerItem)))
	mux.Handle("/api/v1/networks/{id}/status", middleware.AuthRequired(s.auth, http.HandlerFunc(s.handleNetworkStatus)))
}

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}

	var tenantID *uuid.UUID
	if principal.Role == auth.RolePlatformAdmin {
		if raw := r.URL.Query().Get("tenant_id"); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "tenant_id must be a UUID")
				return
			}
			tenantID = &id
		}
	} else {
		tenantID = &principal.TenantID
	}

	var userID *uuid.UUID
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "user_id must be a UUID")
			return
		}
		userID = &id
	}
	from, ok := parseOptionalRFC3339(w, r.URL.Query(), "from")
	if !ok {
		return
	}
	to, ok := parseOptionalRFC3339(w, r.URL.Query(), "to")
	if !ok {
		return
	}
	if from != nil && to != nil && from.After(*to) {
		writeError(w, http.StatusBadRequest, "invalid_request", "from must not be after to")
		return
	}
	page, pageSize, err := auditPagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}

	result, err := s.auditLogs.List(r.Context(), service.AuditLogFilter{
		TenantID: tenantID,
		UserID:   userID,
		Action:   r.URL.Query().Get("action"),
		Resource: r.URL.Query().Get("resource"),
		From:     from,
		To:       to,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "node ID")
	if !ok {
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	windowHours := service.DefaultNodeMetricsWindowHours
	if raw := r.URL.Query().Get("window_hours"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 168 {
			writeError(w, http.StatusBadRequest, "invalid_request", "window_hours must be between 1 and 168")
			return
		}
		windowHours = value
	}
	result, err := s.observability.NodeMetrics(r.Context(), id, s.userScope(principal), windowHours)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRelayServers(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		result, err := s.relays.List(r.Context())
		if err != nil {
			s.internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		if !canManageRelayServers(principal.Role) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.RelayServerInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.relays.Create(r.Context(), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleRelayServerItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if !canManageRelayServers(principal.Role) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "relay server ID")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input service.RelayServerInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.relays.Update(r.Context(), id, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := s.relays.Delete(r.Context(), id); err != nil {
			s.serviceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	result, err := s.observability.NetworkStatus(r.Context(), id, s.userScope(principal))
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func canManageRelayServers(role string) bool {
	return role == auth.RolePlatformAdmin || role == auth.RoleTenantAdmin
}

func parseOptionalRFC3339(w http.ResponseWriter, values url.Values, name string) (*time.Time, bool) {
	raw := values.Get(name)
	if raw == "" {
		return nil, true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", name+" must be an RFC3339 timestamp")
		return nil, false
	}
	value = value.UTC()
	return &value, true
}

func auditPagination(r *http.Request) (int, int, error) {
	page := 1
	pageSize := 50
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || page < 1 {
			return 0, 0, errPagination("page must be a positive integer")
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || pageSize < 1 || pageSize > 200 {
			return 0, 0, errPagination("page_size must be between 1 and 200")
		}
	}
	return page, pageSize, nil
}

type paginationError string

func (e paginationError) Error() string { return string(e) }

func errPagination(message string) error { return paginationError(message) }
