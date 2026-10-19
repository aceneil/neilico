package api

import (
	"encoding/json"

	"neilico/control-plane/internal/auth"
	"net/http"
	"strings"

	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/webproxy"
)

type webProxySiteRequest struct {
	Kind      string `json:"kind"`
	Runtime   string `json:"runtime"`
	Name      string `json:"name"`
	Domain    string `json:"domain"`
	Target    string `json:"target"`
	TLS       bool   `json:"tls"`
	WebSocket bool   `json:"websocket"`
	DryRun    *bool  `json:"dry_run"`
}

// registerWebProxy 注册「宿主反向代理接管」相关路由。
func (s *Server) registerWebProxy(mux *http.ServeMux) {
	admin := []string{auth.RolePlatformAdmin, auth.RoleTenantAdmin}
	mux.Handle("/api/v1/web-proxy/detect", s.requireRole(s.handleWebProxyDetect, admin...))
	mux.Handle("/api/v1/web-proxy/preview", s.requireRole(s.handleWebProxyPreview, admin...))
	mux.Handle("/api/v1/web-proxy/apply", s.requireRole(s.handleWebProxyApply, admin...))
}

// handleWebProxyDetect 探测宿主机上可管理的 Nginx / Caddy / Traefik。
func (s *Server) handleWebProxyDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, webproxy.Detect(r.Context()))
}

// handleWebProxyPreview 只生成配置并返回文本，不写盘。
func (s *Server) handleWebProxyPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	req, candidate, ok := s.webProxyRequest(w, r)
	if !ok {
		return
	}
	site := webproxy.Site{Domain: req.Domain, Target: req.Target, TLS: req.TLS, WebSocket: req.WebSocket}
	content, err := webproxy.Render(candidate, site)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidate": candidate,
		"content":   content,
		"path":      webproxy.ManagedPath(candidate, req.Domain),
	})
}

// handleWebProxyApply 写配置 → 语法校验 → 热重载（dry_run 默认 true）。
func (s *Server) handleWebProxyApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	req, candidate, ok := s.webProxyRequest(w, r)
	if !ok {
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	site := webproxy.Site{Domain: req.Domain, Target: req.Target, TLS: req.TLS, WebSocket: req.WebSocket}
	plan, err := webproxy.Apply(r.Context(), candidate, site, dryRun)
	if err != nil {
		writeErrorDetail(w, http.StatusConflict, "apply_failed", err.Error(), map[string]any{
			"dry_run":    dryRun,
			"validated":  plan.Validated,
			"validation": plan.Validation,
		})
		return
	}
	if !dryRun {
		middleware.SetAuditAction(r.Context(), "web_proxy.apply", "/api/v1/web-proxy/apply", map[string]any{
			"kind":    string(candidate.Kind),
			"runtime": string(candidate.Runtime),
			"name":    candidate.Name,
			"domain":  req.Domain,
			"path":    plan.Path,
		})
	}
	writeJSON(w, http.StatusOK, plan)
}

// webProxyRequest 解码请求并解析出目标 candidate。
func (s *Server) webProxyRequest(w http.ResponseWriter, r *http.Request) (webProxySiteRequest, webproxy.Candidate, bool) {
	var req webProxySiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "请求体不是合法 JSON")
		return req, webproxy.Candidate{}, false
	}
	req.Domain = strings.TrimSpace(req.Domain)
	if req.Domain == "" || strings.TrimSpace(req.Target) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "domain 与 target 必填")
		return req, webproxy.Candidate{}, false
	}
	detected := webproxy.Detect(r.Context())
	for _, c := range detected.Candidates {
		if string(c.Kind) == req.Kind && string(c.Runtime) == req.Runtime && c.Name == req.Name {
			return req, c, true
		}
	}
	writeErrorDetail(w, http.StatusNotFound, "proxy_not_found",
		"未找到该反向代理，请先调用 /api/v1/web-proxy/detect", map[string]any{"requested": req})
	return req, webproxy.Candidate{}, false
}
