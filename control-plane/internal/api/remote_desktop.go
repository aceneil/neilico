package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/service"
)

// RemoteDesktopOptions 承载「远程桌面」模块的启动配置（来自 config.RemoteDesktop）。
type RemoteDesktopOptions struct {
	Enabled       bool
	IDServer      string
	RelayServer   string
	PublicKeyFile string
	Ports         []int
	// 自托管 hbbs/hbbr 生命周期（本轮新增）。
	ServerMode  string
	IdleTimeout time.Duration
	KeyDir      string
	HBBSPath    string
	HBBRPath    string
	RelayHost   string
	RelayPort   int
	UDPPort     int
}

// registerRemoteDesktop 挂载「远程桌面」相关接口。
//
//	GET  /api/v1/remote-desktop/config   登录可读：服务器参数（**只含公钥**）
//	PUT  /api/v1/remote-desktop/config   仅 platform_admin：修改 id/relay/enabled
//	GET  /api/v1/remote-desktop/devices  登录可读：设备列表 + 连接参数
//	GET  /api/v1/remote-desktop/status   登录可读：服务器端口探活
//	GET   /api/v1/remote-desktop/device-policies         登录可读：每台设备授权状态
//	PATCH /api/v1/remote-desktop/device-policies/{id}    仅 platform_admin / tenant_admin：局部更新
//	POST  /api/v1/remote-desktop/register                 设备运行时注册（校验三态与策略新鲜度）
//	POST  /api/v1/remote-desktop/heartbeat                设备运行时心跳（每轮重新拉取策略）
//	GET   /api/v1/remote-desktop/server-status           登录可读：自托管服务端状态（运行/停止/空闲倒计时）
//	POST  /api/v1/remote-desktop/server/start            仅 platform_admin / tenant_admin：手动拉起
//	POST  /api/v1/remote-desktop/server/stop             仅 platform_admin / tenant_admin：手动停止
func (s *Server) registerRemoteDesktop(mux *http.ServeMux) {
	mux.Handle("/api/v1/remote-desktop/config", s.authed(http.HandlerFunc(s.handleRemoteDesktopConfig)))
	mux.Handle("/api/v1/remote-desktop/devices", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevices)))
	mux.Handle("/api/v1/remote-desktop/status", s.authed(http.HandlerFunc(s.handleRemoteDesktopStatus)))
	mux.Handle("/api/v1/remote-desktop/device-policies", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevicePolicies)))
	mux.Handle("/api/v1/remote-desktop/device-policies/", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevicePolicyItem)))
	mux.HandleFunc("/api/v1/remote-desktop/register", s.handleRemoteDesktopRuntime)
	mux.HandleFunc("/api/v1/remote-desktop/heartbeat", s.handleRemoteDesktopRuntime)
	mux.Handle("/api/v1/remote-desktop/server-status", s.authed(http.HandlerFunc(s.handleRemoteDesktopServerStatus)))
	mux.Handle("/api/v1/remote-desktop/server/", s.authed(http.HandlerFunc(s.handleRemoteDesktopServerAction)))
}

// handleRemoteDesktopServerStatus：GET 自托管 hbbs/hbbr 的实时状态。
func (s *Server) handleRemoteDesktopServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	if _, ok := middleware.PrincipalFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	writeJSON(w, http.StatusOK, s.rustdesk.Status())
}

// handleRemoteDesktopServerAction：POST start/stop（admin）。手动动作进入「手动保持」，不受空闲回收影响。
func (s *Server) handleRemoteDesktopServerAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/remote-desktop/server/")
	switch action {
	case "start":
		if err := s.rustdesk.Start(r.Context(), true); err != nil {
			if errors.Is(err, service.ErrRustDeskServerDisabled) {
				writeError(w, http.StatusConflict, "server_disabled", err.Error())
				return
			}
			s.internalError(w, err)
			return
		}
		middleware.SetAuditAction(r.Context(), "remote_desktop.server.start", r.URL.Path, nil)
	case "stop":
		if err := s.rustdesk.Stop(r.Context()); err != nil {
			s.internalError(w, err)
			return
		}
		middleware.SetAuditAction(r.Context(), "remote_desktop.server.stop", r.URL.Path, nil)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown server action")
		return
	}
	writeJSON(w, http.StatusOK, s.rustdesk.Status())
}

func (s *Server) handleRemoteDesktopConfig(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg := s.remoteDesktop.Config(r.Context())
		if rawNodeID := strings.TrimSpace(r.URL.Query().Get("node_id")); rawNodeID != "" {
			nodeID, err := parseID(rawNodeID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_id", "node ID must be a UUID")
				return
			}
			policyView, err := s.rdPolicies.Get(r.Context(), nodeID, s.userScope(principal))
			if err != nil {
				s.serviceError(w, err)
				return
			}
			state := service.FeatureState(policyView.Features[service.FeatureRemoteDesktop])
			if !state.Allowed() {
				s.logRemoteDesktopPolicyReject(r.Context(), "config", nodeID, policyView, "policy_denied")
				writeErrorDetail(w, http.StatusForbidden, "policy_denied",
					"remote_desktop is denied by server device policy", map[string]any{"policy": policyView})
				return
			}
			cfg.Policy = &policyView
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		// 服务器参数是全局基础设施，仅平台管理员可改。
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.RemoteDesktopUpdateInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		if err := s.remoteDesktop.Update(r.Context(), input); err != nil {
			s.serviceError(w, err)
			return
		}
		// 沿用现有审计：中间件记录 action/resource，这里补上被修改的字段名。
		middleware.SetAuditAction(r.Context(), "remote_desktop.config.update", "/api/v1/remote-desktop/config", map[string]any{
			"enabled":      input.Enabled != nil,
			"id_server":    input.IDServer != nil,
			"relay_server": input.RelayServer != nil,
		})
		writeJSON(w, http.StatusOK, s.remoteDesktop.Config(r.Context()))
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
}

func (s *Server) handleRemoteDesktopDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	tenantID := s.userScope(principal)
	list, err := s.nodes.List(r.Context(), tenantID, service.NodeListFilter{Page: 1, PageSize: 100})
	if err != nil {
		s.internalError(w, err)
		return
	}
	cfg := s.remoteDesktop.Config(r.Context())
	writeJSON(w, http.StatusOK, service.BuildRemoteDesktopDevices(list.Items, cfg))
}

func (s *Server) handleRemoteDesktopStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	if _, ok := middleware.PrincipalFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	writeJSON(w, http.StatusOK, s.remoteDesktop.Probe(r.Context()))
}

// handleRemoteDesktopDevicePolicies：GET 每台设备的授权状态（任何登录用户，只读自身租户）。
func (s *Server) handleRemoteDesktopDevicePolicies(w http.ResponseWriter, r *http.Request) {
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
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRemoteDesktopDevicePolicyItem：PATCH 单台设备的授权开关（仅 admin）。局部更新。
func (s *Server) handleRemoteDesktopDevicePolicyItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		s.methodNotAllowed(w, http.MethodPatch)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	// 授权变更是管理动作：platform_admin / tenant_admin 可改，普通用户 403。
	if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, "/api/v1/remote-desktop/device-policies/")
	nodeID, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "node ID must be a UUID")
		return
	}
	var input service.RemoteDesktopPolicyPatchInput
	if !s.decodeRequest(w, r, &input) {
		return
	}
	result, err := s.rdPolicies.PatchWithAudit(r.Context(), nodeID, s.userScope(principal), input)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	// 沿用现有审计中间件：记录 from/to（不记录任何密钥）。
	auditDetail := result.AuditDetail
	auditDetail["remote_control_allowed"] = input.RemoteControlAllowed != nil
	auditDetail["tunnel_mode"] = input.TunnelMode != nil
	auditDetail["isolated_tunnel_enabled"] = input.IsolatedTunnelEnabled != nil
	auditDetail["mesh_joined"] = input.MeshJoined != nil
	middleware.SetAuditAction(r.Context(), "remote_desktop.policy.update", r.URL.Path, auditDetail)
	// 单独隧道可能新增/删除转发规则：让转发引擎立即与数据库对齐。
	s.ReconcileStreams(r.Context())
	// 有远程桌面活动（授权/隧道/Mesh 开启）时按需拉起自托管 hbbs/hbbr。
	if (input.RemoteControlAllowed != nil && *input.RemoteControlAllowed) ||
		(input.IsolatedTunnelEnabled != nil && *input.IsolatedTunnelEnabled) ||
		(input.MeshJoined != nil && *input.MeshJoined) {
		s.rustdesk.Trigger(r.Context())
	}
	// 回显**更新后的完整对象**（契约：`{ "item": { ...同 GET 单项... } }`）——
	// 客户端据此原地刷新开关，无需再补发一次 GET；绝不可回 null/空对象。
	writeJSON(w, http.StatusOK, map[string]any{"item": result.Item})
}

type remoteDesktopRuntimeRequest struct {
	NodeID          uuid.UUID `json:"node_id"`
	PolicyVersion   int64     `json:"policy_version"`
	PolicyFetchedAt time.Time `json:"policy_fetched_at"`
}

// handleRemoteDesktopRuntime 处理远程桌面运行时的 register/heartbeat。
// deny 在服务端直接拒绝；过期策略返回 409，客户端必须重新 GET 策略后重试。
func (s *Server) handleRemoteDesktopRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input remoteDesktopRuntimeRequest
	if !s.decodeRequest(w, r, &input) {
		return
	}
	if input.NodeID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "node_id is required")
		return
	}
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "agent token or access token required")
		return
	}
	var tenantID *uuid.UUID
	if claims, err := s.auth.Parse(token, auth.TokenAccess); err == nil {
		if claims.Role != auth.RolePlatformAdmin {
			tenantID = &claims.TenantID
		}
		if _, err := s.nodes.Get(r.Context(), input.NodeID, tenantID); err != nil {
			s.serviceError(w, err)
			return
		}
	} else if _, err := s.nodes.AuthenticateNode(r.Context(), input.NodeID, token); errors.Is(err, service.ErrForbidden) {
		writeError(w, http.StatusUnauthorized, "invalid_agent_token", "valid agent token required")
		return
	} else if err != nil {
		s.serviceError(w, err)
		return
	}

	policy, err := s.rdPolicies.Get(r.Context(), input.NodeID, tenantID)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	now := time.Now().UTC()
	state := service.FeatureState(policy.Features[service.FeatureRemoteDesktop])
	if !state.Allowed() {
		s.logRemoteDesktopPolicyReject(r.Context(), r.URL.Path, input.NodeID, policy, "policy_denied")
		writeErrorDetail(w, http.StatusForbidden, "policy_denied",
			"remote_desktop is denied by server device policy", map[string]any{"policy": policy})
		return
	}
	if reason := remoteDesktopPolicyFreshnessError(policy, input, now); reason != "" {
		s.logRemoteDesktopPolicyReject(r.Context(), r.URL.Path, input.NodeID, policy, "policy_stale")
		writeErrorDetail(w, http.StatusConflict, "policy_stale",
			"remote desktop client must refresh the device policy", map[string]any{
				"reason":                  reason,
				"policy":                  policy,
				"min_cache_ttl_seconds":   policy.MinCacheTTLSeconds,
				"policy_refresh_required": true,
			})
		return
	}

	s.logger.Info("remote desktop runtime accepted",
		"event_time", now.Format(time.RFC3339Nano),
		"path", r.URL.Path,
		"node_id", input.NodeID.String(),
		"feature_state", state,
		"policy_version", policy.PolicyVersion,
		"policy_updated_at", policy.PolicyUpdatedAt.Format(time.RFC3339Nano),
		"policy_fetched_at", input.PolicyFetchedAt.UTC().Format(time.RFC3339Nano),
	)
	response := map[string]any{
		"active":                  true,
		"server_time":             now,
		"policy":                  policy,
		"policy_refresh_required": true,
		"min_cache_ttl_seconds":   policy.MinCacheTTLSeconds,
	}
	if strings.HasSuffix(r.URL.Path, "/heartbeat") {
		response["next_heartbeat_seconds"] = 30
	} else {
		response["registered_at"] = now
	}
	writeJSON(w, http.StatusOK, response)
}

func remoteDesktopPolicyFreshnessError(policy service.DevicePolicyView, input remoteDesktopRuntimeRequest, now time.Time) string {
	if input.PolicyVersion != policy.PolicyVersion {
		return "policy_version is stale"
	}
	if input.PolicyFetchedAt.IsZero() {
		return "policy_fetched_at is required"
	}
	fetchedAt := input.PolicyFetchedAt.UTC()
	if fetchedAt.After(now.Add(5 * time.Second)) {
		return "policy_fetched_at is in the future"
	}
	if now.Sub(fetchedAt) > service.PolicyMinCacheTTL {
		return "policy cache exceeded min_cache_ttl"
	}
	if fetchedAt.Before(policy.PolicyUpdatedAt.Add(-5 * time.Second)) {
		return "policy was fetched before the current policy version"
	}
	return ""
}

func (s *Server) logRemoteDesktopPolicyReject(ctx context.Context, action string, nodeID uuid.UUID, policy service.DevicePolicyView, reason string) {
	now := time.Now().UTC()
	s.logger.Warn("remote desktop runtime rejected by device policy",
		"event_time", now.Format(time.RFC3339Nano),
		"path", action,
		"node_id", nodeID.String(),
		"reason", reason,
		"policy_version", policy.PolicyVersion,
		"policy_updated_at", policy.PolicyUpdatedAt.Format(time.RFC3339Nano),
	)
}
