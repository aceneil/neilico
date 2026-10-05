package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
	"neilico/control-plane/internal/service/proxy"
)

// ReconcileStreams 供进程启动时调用，把数据库里的端口转发规则装载进转发引擎。
func (h *Handler) ReconcileStreams(ctx context.Context) { h.server.ReconcileStreams(ctx) }

// CloseStreams 供进程退出时调用，释放监听端口。
func (h *Handler) CloseStreams() { h.server.CloseStreams() }

// StreamRuleView 是给前端的视图：数据库里的规则 + 转发引擎的运行时状态。
type StreamRuleView struct {
	models.StreamRule
	Status            string `json:"status"`
	LastError         string `json:"last_error,omitempty"`
	ActiveConnections int64  `json:"active_connections"`
	BytesIn           int64  `json:"bytes_in"`
	BytesOut          int64  `json:"bytes_out"`
}

func (s *Server) handleStreamRules(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, pageSize, err := pagination(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.streamRules.List(r.Context(), s.userScope(principal), page, pageSize)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		stats := s.streamRuntimeStats()
		problems := s.streamRuleProblems()
		views := make([]StreamRuleView, 0, len(result.Items))
		for _, item := range result.Items {
			view := StreamRuleView{StreamRule: item, Status: "disabled"}
			if item.Enabled {
				view.Status = "pending"
			}
			if runtime, found := stats[item.ID]; found {
				view.Status = runtime.Status
				view.LastError = runtime.LastError
				view.ActiveConnections = runtime.ActiveConnections
				view.BytesIn = runtime.BytesIn
				view.BytesOut = runtime.BytesOut
			} else if item.Enabled {
				if reason, found := problems[item.ID]; found {
					view.Status = "error"
					view.LastError = reason
				}
			}
			views = append(views, view)
		}
		minPort, maxPort := s.streamRules.PortRange()
		writeJSON(w, http.StatusOK, map[string]any{
			"items": views, "total": result.Total, "page": result.Page, "page_size": result.PageSize,
			"port_range": map[string]int{"min": minPort, "max": maxPort},
		})
	case http.MethodPost:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.StreamRuleInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.streamRules.Create(r.Context(), principal.TenantID, input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		s.ReconcileStreams(r.Context())
		writeJSON(w, http.StatusCreated, item)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleStreamRuleItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/v1/stream-rules/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "stream rule ID must be a UUID")
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := s.streamRules.Get(r.Context(), id, s.userScope(principal))
		if err != nil {
			s.serviceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		var input service.StreamRuleInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		item, err := s.streamRules.Update(r.Context(), id, s.userScope(principal), input)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		s.ReconcileStreams(r.Context())
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !canManageProxy(r.Context(), principal) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		if err := s.streamRules.Delete(r.Context(), id, s.userScope(principal)); err != nil {
			s.serviceError(w, err)
			return
		}
		s.ReconcileStreams(r.Context())
		w.WriteHeader(http.StatusNoContent)
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

// ReconcileStreams 让转发引擎与数据库中的启用规则保持一致。规则变更后立即调用，
// 使端口转发「保存即生效」（不必等配置轮询）。
func (s *Server) ReconcileStreams(ctx context.Context) {
	if s.streams == nil || s.streamRules == nil {
		return
	}
	resolved, problems := s.streamRules.Resolved(ctx, nil)
	rules := make([]proxy.StreamRule, 0, len(resolved))
	for _, item := range resolved {
		rules = append(rules, proxy.StreamRule{
			ID:          item.Rule.ID,
			TenantID:    item.Rule.TenantID,
			Name:        item.Rule.Name,
			Protocol:    item.Rule.Protocol,
			ListenPort:  item.Rule.ListenPort,
			TargetHost:  item.Host,
			TargetPort:  item.Port,
			IPWhitelist: append([]string(nil), item.Rule.IPWhitelist...),
		})
	}
	s.streams.Apply(ctx, rules)

	for id, reason := range problems {
		s.logger.Warn("stream rule skipped", "rule_id", id, "error", reason)
	}
	s.streamMu.Lock()
	s.streamProblems = problems
	s.streamMu.Unlock()
}

// CloseStreams 在进程退出前释放监听端口。
func (s *Server) CloseStreams() {
	if s.streams != nil {
		s.streams.Close()
	}
}

func (s *Server) streamRuntimeStats() map[uuid.UUID]proxy.StreamStats {
	if s.streams == nil {
		return nil
	}
	return s.streams.Stats()
}

func (s *Server) streamRuleProblems() map[uuid.UUID]string {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if len(s.streamProblems) == 0 {
		return nil
	}
	problems := make(map[uuid.UUID]string, len(s.streamProblems))
	for id, reason := range s.streamProblems {
		problems[id] = reason
	}
	return problems
}
