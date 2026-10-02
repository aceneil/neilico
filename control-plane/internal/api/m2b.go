package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/middleware"
	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service"
	configservice "umpp/control-plane/internal/service/config"
)

func (s *Server) registerM2B(mux *http.ServeMux) {
	mux.Handle("/api/v1/networks", s.authed(http.HandlerFunc(s.handleNetworks)))
	mux.Handle("/api/v1/networks/{id}", s.authed(http.HandlerFunc(s.handleNetworkItem)))
	mux.Handle("/api/v1/networks/{id}/members", s.authed(http.HandlerFunc(s.handleNetworkMembers)))
	mux.Handle("/api/v1/networks/{id}/members/{node_id}", s.authed(http.HandlerFunc(s.handleNetworkMemberItem)))
	mux.Handle("/api/v1/networks/{id}/acl", s.authed(http.HandlerFunc(s.handleNetworkACL)))
	mux.Handle("/api/v1/networks/{id}/acl/{rule_id}", s.authed(http.HandlerFunc(s.handleNetworkACLItem)))
	mux.Handle("/api/v1/networks/{id}/routes", s.authed(http.HandlerFunc(s.handleNetworkRoutes)))
	mux.Handle("/api/v1/networks/{id}/routes/{route_id}", s.authed(http.HandlerFunc(s.handleNetworkRouteItem)))
	mux.Handle("/api/v1/networks/{id}/mesh/export", s.authed(http.HandlerFunc(s.handleMeshExport)))
	mux.Handle("/api/v1/networks/{id}/psk/rotate", s.authed(http.HandlerFunc(s.handleNetworkPSKRotate)))

	mux.Handle("/api/v1/nodes/{id}/keys/rotate", s.authed(http.HandlerFunc(s.handleNodeKeyRotate)))
	mux.HandleFunc("/api/v1/nodes/{id}/network-report", s.handleNetworkReport)
	mux.HandleFunc("/api/v1/agent/config", s.handleAgentConfig)
	mux.Handle("/api/v1/configs", s.authed(http.HandlerFunc(s.handleConfigVersions)))
	mux.Handle("/api/v1/configs/{target_type}/{target_id}/rollback", s.authed(http.HandlerFunc(s.handleConfigRollback)))
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.networks.List(r.Context(), s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.NetworkInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.networks.Create(r.Context(), principal.TenantID, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if _, err := s.configs.BumpForNetwork(r.Context(), item.ID, "network created"); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleNetworkItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.networks.Get(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.NetworkInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.networks.Update(r.Context(), id, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpNetwork(r.Context(), id, "network updated"); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		members, err := s.networks.ListMembers(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.networks.Delete(r.Context(), id, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		for _, member := range members.Items {
			if _, err := s.configs.BumpForNode(r.Context(), member.NodeID, "network deleted"); err != nil {
				s.logger.Error("failed to bump node after network deletion", "node_id", member.NodeID, "error", err)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleNetworkMembers(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.networks.ListMembers(r.Context(), networkID, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.NetworkMemberInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.networks.AddMember(r.Context(), networkID, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpNetwork(r.Context(), networkID, "network member added"); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleNetworkMemberItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if r.Method != http.MethodDelete {
		s.methodNotAllowed(w, http.MethodDelete)
		return
	}
	if !canManageNetworks(r.Context(), principal) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	nodeID, ok := s.pathID(w, r.PathValue("node_id"), "node ID")
	if !ok {
		return
	}
	if err := s.networks.RemoveMember(r.Context(), networkID, nodeID, s.userScope(principal)); err != nil {
		s.serviceError(w, err)
		return
	}
	if err := s.bumpNetwork(r.Context(), networkID, "network member removed"); err != nil {
		s.serviceError(w, err)
		return
	}
	if _, err := s.configs.BumpForNode(r.Context(), nodeID, "network member removed"); err != nil {
		s.serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNetworkACL(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.networks.ListACL(r.Context(), networkID, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.ACLInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.networks.CreateACL(r.Context(), networkID, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpNetwork(r.Context(), networkID, "ACL rule added"); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleNetworkACLItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if r.Method != http.MethodDelete {
		s.methodNotAllowed(w, http.MethodDelete)
		return
	}
	if !canManageNetworks(r.Context(), principal) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	ruleID, ok := s.pathID(w, r.PathValue("rule_id"), "ACL rule ID")
	if !ok {
		return
	}
	if err := s.networks.DeleteACL(r.Context(), networkID, ruleID, s.userScope(principal)); err != nil {
		s.serviceError(w, err)
		return
	}
	if err := s.bumpNetwork(r.Context(), networkID, "ACL rule removed"); err != nil {
		s.serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNetworkRoutes(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.networks.ListRoutes(r.Context(), networkID, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.RouteInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.networks.CreateRoute(r.Context(), networkID, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpNetwork(r.Context(), networkID, "subnet route added"); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleNetworkRouteItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	routeID, ok := s.pathID(w, r.PathValue("route_id"), "route ID")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPut:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.RouteInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.networks.UpdateRoute(r.Context(), networkID, routeID, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpNetwork(r.Context(), networkID, "subnet route updated"); err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !canManageNetworks(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		if err := s.networks.DeleteRoute(r.Context(), networkID, routeID, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		if err := s.bumpNetwork(r.Context(), networkID, "subnet route removed"); err != nil {
			s.serviceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleMeshExport(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "tenant administrator role required")
		return
	}
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	networkID, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	data, err := s.configs.RenderExport(r.Context(), networkID)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/toml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleNodeKeyRotate(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	nodeID, ok := s.pathID(w, r.PathValue("id"), "node ID")
	if !ok {
		return
	}
	var scope *uuid.UUID
	if principal.Role != auth.RolePlatformAdmin {
		scope = &principal.TenantID
	}
	result, err := s.nodes.RotateKey(r.Context(), nodeID, scope)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	var memberships []models.NetworkMember
	if err := s.db.WithContext(r.Context()).Where("node_id = ?", nodeID).Find(&memberships).Error; err != nil {
		s.internalError(w, fmt.Errorf("load key rotation memberships: %w", err))
		return
	}
	if len(memberships) > 0 {
		for _, membership := range memberships {
			if err := s.bumpNetwork(r.Context(), membership.NetworkID, "WireGuard key rotated"); err != nil {
				s.serviceError(w, err)
				return
			}
		}
	} else if _, err := s.configs.BumpForNode(r.Context(), nodeID, "WireGuard key rotated"); err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleNetworkPSKRotate(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	if !canManageNetworks(r.Context(), principal) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "network ID")
	if !ok {
		return
	}
	psk, err := s.networks.RotatePSK(r.Context(), id, s.userScope(principal))
	if err != nil {
		s.serviceError(w, err)
		return
	}
	if err := s.bumpNetwork(r.Context(), id, "WireGuard preshared key rotated"); err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preshared_key": psk})
}

func (s *Server) handleNetworkReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	nodeID, ok := s.pathID(w, r.PathValue("id"), "node ID")
	if !ok {
		return
	}
	var input service.NetworkReportInput
	if !s.decodeRequest(w, r, &input) {
		return
	}
	node, err := s.nodes.NetworkReport(r.Context(), nodeID, bearerToken(r), input)
	if errors.Is(err, service.ErrForbidden) {
		writeError(w, http.StatusForbidden, "invalid_agent_token", "agent token does not match node")
		return
	}
	if err != nil {
		s.serviceError(w, err)
		return
	}
	var memberships []models.NetworkMember
	if err := s.db.WithContext(r.Context()).Where("node_id = ?", node.ID).Find(&memberships).Error; err != nil {
		s.internalError(w, fmt.Errorf("load reported node memberships: %w", err))
		return
	}
	for _, membership := range memberships {
		s.metrics.SetTunnelUp(membership.NetworkID.String(), node.ID.String(), true)
		if err := s.bumpNetwork(r.Context(), membership.NetworkID, "node public endpoint updated"); err != nil {
			s.serviceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": node})
}

func (s *Server) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	rawNodeID := r.URL.Query().Get("node_id")
	nodeID, ok := s.pathID(w, rawNodeID, "node ID")
	if !ok {
		return
	}
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "agent token or access token required")
		return
	}
	includePrivate := false
	claims, jwtErr := s.auth.Parse(token, auth.TokenAccess)
	if jwtErr == nil {
		if !auth.RoleAllowed(claims.Role, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "tenant administrator role required")
			return
		}
		var node models.Node
		err := s.db.WithContext(r.Context()).Where("id = ?", nodeID).First(&node).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && claims.Role != auth.RolePlatformAdmin && node.TenantID != claims.TenantID) {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		if err != nil {
			s.internalError(w, err)
			return
		}
	} else {
		if _, err := s.nodes.AuthenticateNode(r.Context(), nodeID, token); errors.Is(err, service.ErrForbidden) {
			writeError(w, http.StatusForbidden, "invalid_agent_token", "agent token does not match node")
			return
		} else if err != nil {
			s.serviceError(w, err)
			return
		}
		includePrivate = true
	}
	requestedVersion := 0
	if raw := r.URL.Query().Get("version"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		requestedVersion = parsed
	}
	delivery, notModified, err := s.configs.Delivery(r.Context(), nodeID, requestedVersion, includePrivate)
	if err != nil {
		if recordErr := s.configs.RecordDispatchFailure(r.Context(), nodeID, err); recordErr != nil {
			s.logger.Error("record config dispatch failure", "node_id", nodeID, "error", recordErr)
		}
		s.serviceError(w, err)
		return
	}
	if recordErr := s.configs.RecordDispatchSuccess(r.Context(), nodeID); recordErr != nil {
		s.logger.Error("clear config dispatch failure", "node_id", nodeID, "error", recordErr)
	}
	if notModified {
		writeJSON(w, http.StatusNotModified, map[string]any{"not_modified": true, "version": delivery.Version})
		return
	}
	writeJSON(w, http.StatusOK, delivery)
}

func (s *Server) handleConfigVersions(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "tenant administrator role required")
		return
	}
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	targetType := r.URL.Query().Get("target_type")
	if targetType != configservice.TargetNode && targetType != configservice.TargetNetwork && targetType != configservice.TargetProxy {
		writeError(w, http.StatusBadRequest, "invalid_request", "target_type must be node, network, or proxy")
		return
	}
	targetID, ok := s.pathID(w, r.URL.Query().Get("target_id"), "target ID")
	if !ok {
		return
	}
	list, err := s.configs.ListVersions(r.Context(), s.userScope(principal), targetType, targetID)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok || !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "tenant administrator role required")
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	targetType := r.PathValue("target_type")
	if targetType != configservice.TargetNode && targetType != configservice.TargetNetwork && targetType != configservice.TargetProxy {
		writeError(w, http.StatusBadRequest, "invalid_request", "target_type must be node, network, or proxy")
		return
	}
	targetID, ok := s.pathID(w, r.PathValue("target_id"), "target ID")
	if !ok {
		return
	}
	var input struct {
		Version *int `json:"version"`
	}
	if err := decodeJSON(w, r, &input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return
	}
	sourceVersion := 0
	if input.Version != nil {
		sourceVersion = *input.Version
	} else if raw := r.URL.Query().Get("version"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "version must be an integer")
			return
		}
		sourceVersion = parsed
	}
	if sourceVersion < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "version must be positive")
		return
	}
	created, err := s.configs.Rollback(r.Context(), s.userScope(principal), targetType, targetID, sourceVersion)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	s.writeRollbackAudit(r, principal, targetType, targetID, sourceVersion, created.Version)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) writeRollbackAudit(r *http.Request, principal middleware.Principal, targetType string, targetID uuid.UUID, sourceVersion, createdVersion int) {
	detailJSON, _ := json.Marshal(map[string]any{
		"target_type":     targetType,
		"target_id":       targetID,
		"source_version":  sourceVersion,
		"created_version": createdVersion,
	})
	userID := principal.UserID
	tenantID := principal.TenantID
	entry := models.AuditLog{
		ID: uuid.New(), TenantID: &tenantID, UserID: &userID,
		Action: "config.rollback", Resource: fmt.Sprintf("%s/%s", targetType, targetID),
		Detail: datatypes.JSON(detailJSON), IP: auditClientIP(r),
	}
	if err := s.db.WithContext(r.Context()).Create(&entry).Error; err != nil {
		s.logger.Error("failed to write config rollback audit", "error", err)
	}
}

func auditClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "0.0.0.0"
}

func (s *Server) bumpNetwork(ctx context.Context, networkID uuid.UUID, reason string) error {
	_, err := s.configs.BumpForNetwork(ctx, networkID, reason)
	return err
}

func (s *Server) bumpTenantNodes(ctx context.Context, tenantID uuid.UUID, reason string) error {
	var nodeIDs []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Node{}).Where("tenant_id = ?", tenantID).
		Order("id").Pluck("id", &nodeIDs).Error; err != nil {
		return fmt.Errorf("load tenant nodes for config bump: %w", err)
	}
	for _, nodeID := range nodeIDs {
		if _, err := s.configs.BumpForNode(ctx, nodeID, reason); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) pathID(w http.ResponseWriter, raw, label string) (uuid.UUID, bool) {
	id, err := parseID(strings.TrimSpace(raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", label+" must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func canManageNetworks(ctx context.Context, principal middleware.Principal) bool {
	return roleAllowed(ctx, principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps)
}
