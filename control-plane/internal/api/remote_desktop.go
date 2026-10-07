package api

import (
	"net/http"

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
}

// registerRemoteDesktop 挂载「远程桌面」相关接口。
//
//	GET  /api/v1/remote-desktop/config   登录可读：服务器参数（**只含公钥**）
//	PUT  /api/v1/remote-desktop/config   仅 platform_admin：修改 id/relay/enabled
//	GET  /api/v1/remote-desktop/devices  登录可读：设备列表 + 连接参数
//	GET  /api/v1/remote-desktop/status   登录可读：服务器端口探活
func (s *Server) registerRemoteDesktop(mux *http.ServeMux) {
	mux.Handle("/api/v1/remote-desktop/config", s.authed(http.HandlerFunc(s.handleRemoteDesktopConfig)))
	mux.Handle("/api/v1/remote-desktop/devices", s.authed(http.HandlerFunc(s.handleRemoteDesktopDevices)))
	mux.Handle("/api/v1/remote-desktop/status", s.authed(http.HandlerFunc(s.handleRemoteDesktopStatus)))
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
