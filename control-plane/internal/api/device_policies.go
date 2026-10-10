package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/service"
)

// registerDevicePolicies 挂载通用、可扩展的每设备能力策略 API。
//
//	GET   /api/v1/device-policies
//	GET   /api/v1/device-policies/{node_id}
//	PATCH /api/v1/device-policies/{node_id}
func (s *Server) registerDevicePolicies(mux *http.ServeMux) {
	mux.Handle("/api/v1/device-policies", s.authed(http.HandlerFunc(s.handleDevicePolicies)))
	mux.Handle("/api/v1/device-policies/", s.authed(http.HandlerFunc(s.handleDevicePolicyItem)))
}

func (s *Server) handleDevicePolicies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	list, err := s.rdPolicies.List(r.Context(), s.userScope(principal))
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDevicePolicyItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	nodeID, ok := s.devicePolicyNodeID(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.rdPolicies.Get(r.Context(), nodeID, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"item": item})
	case http.MethodPatch:
		// 三态策略是管理面硬策略：仅 platform_admin / tenant_admin 可写。
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.DevicePolicyPatchInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		result, err := s.rdPolicies.PatchFeatures(r.Context(), nodeID, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		s.finishDevicePolicyPatch(r, nodeID, result)
		// 回归契约：PATCH 必须回显更新后的完整对象。
		writeJSON(w, http.StatusOK, map[string]any{"item": result.Item})
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func (s *Server) devicePolicyNodeID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	rawID := strings.TrimPrefix(r.URL.Path, "/api/v1/device-policies/")
	nodeID, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "node ID must be a UUID")
		return uuid.Nil, false
	}
	return nodeID, true
}

func (s *Server) finishDevicePolicyPatch(r *http.Request, nodeID uuid.UUID, result service.PolicyPatchResult) {
	middleware.SetAuditAction(r.Context(), "device_policy.update", r.URL.Path, result.AuditDetail)
	s.ReconcileStreams(r.Context())
	after, _ := result.AuditDetail["after"].(map[string]any)
	features, _ := after["features"].(map[string]string)
	if service.FeatureState(features[service.FeatureRemoteDesktop]).Allowed() {
		s.rustdesk.Trigger(r.Context())
	}
}
