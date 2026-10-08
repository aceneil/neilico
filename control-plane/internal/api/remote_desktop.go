package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

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
//	GET   /api/v1/remote-desktop/server-status           登录可读：自托管服务端状态（运行/停止/空闲倒计时）
//	POST  /api/v1/remote-desktop/server/start            仅 platform_admin / tenant_admin：手动拉起
//	POST  /api/v1/remote-desktop/server/stop             仅 platform_admin / tenant_admin：手动停止
func (s *Server) registerRemoteDesktop(mux *http.ServeMux) {
	mux.Handle("/api/v1/remote-desktop/config", s.authed(http.HandlerFunc(s.handleRemoteDesktopConfig)))
	mux.Handle("/api/v1/remote-desktop/devices", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevices)))
	mux.Handle("/api/v1/remote-desktop/status", s.authed(http.HandlerFunc(s.handleRemoteDesktopStatus)))
	mux.Handle("/api/v1/remote-desktop/device-policies", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevicePolicies)))
	mux.Handle("/api/v1/remote-desktop/device-policies/", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevicePolicyItem)))
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
		writeJSON(w, http.StatusOK, s.remoteDesktop.Config(r.Context()))
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
	view, err := s.rdPolicies.Patch(r.Context(), nodeID, s.userScope(principal), input)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	// 沿用现有审计中间件：记录改了哪些字段（不记录任何密钥）。
	middleware.SetAuditAction(r.Context(), "remote_desktop.policy.update", r.URL.Path, map[string]any{
		"node_id":                 nodeID.String(),
		"remote_control_allowed":  input.RemoteControlAllowed != nil,
		"tunnel_mode":             input.TunnelMode != nil,
		"isolated_tunnel_enabled": input.IsolatedTunnelEnabled != nil,
		"mesh_joined":             input.MeshJoined != nil,
	})
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
	writeJSON(w, http.StatusOK, map[string]any{"item": view})
}
