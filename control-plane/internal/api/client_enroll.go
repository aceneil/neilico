package api

import (
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/service"
)

type clientEnrollInput struct {
	Token string `json:"token"`
}

type clientEnrollServer struct {
	IDServer    string `json:"id_server"`
	RelayServer string `json:"relay_server"`
	APIServer   string `json:"api_server"`
	Key         string `json:"key"`
}

type clientEnrollOutput struct {
	NodeID    string             `json:"node_id"`
	NodeName  string             `json:"node_name"`
	Server    clientEnrollServer `json:"server"`
	APIToken  string             `json:"api_token"`
	ExpiresAt *time.Time         `json:"expires_at"`
}

func (s *Server) registerClientEnroll(mux *http.ServeMux) {
	handler := middleware.RateLimit(s.rateLimiter, http.HandlerFunc(s.handleClientEnroll))
	mux.HandleFunc("/api/v1/client/enroll", handler.ServeHTTP)
}

// handleClientEnroll 用一次性接入令牌换取长期 API token：令牌本身即凭据，无需登录。
func (s *Server) handleClientEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input clientEnrollInput
	if !s.decodeRequest(w, r, &input) {
		middleware.SetAuditAction(r.Context(), "client.enroll", "/api/v1/client/enroll", map[string]any{"result": "invalid_request"})
		return
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		middleware.SetAuditAction(r.Context(), "client.enroll", "/api/v1/client/enroll", map[string]any{"result": "invalid_request"})
		writeError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}

	hostname, _ := os.Hostname()
	output, err := s.enrollNodes.Enroll(r.Context(), token, service.NodeEnrollInput{
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Version:  "neilico-client",
	})
	if err != nil {
		result, status, code, message := "unauthorized", http.StatusUnauthorized, "invalid_enroll_token", "invalid enrollment token"
		switch {
		case errors.Is(err, service.ErrEnrollGone):
			result, status, code, message = "exhausted", http.StatusGone, "enroll_token_exhausted", "enrollment token has no remaining uses"
		case errors.Is(err, service.ErrInvalidInput):
			result, status, code, message = "invalid_request", http.StatusBadRequest, "invalid_request", err.Error()
		}
		middleware.SetAuditAction(r.Context(), "client.enroll", "/api/v1/client/enroll", map[string]any{"result": result})
		writeError(w, status, code, message)
		return
	}
	if output.Replayed {
		// 请求指纹相同说明同一个令牌已经被消费过；客户端接入令牌只允许用一次。
		middleware.SetAuditAction(r.Context(), "client.enroll", "/api/v1/client/enroll", map[string]any{"result": "exhausted"})
		writeError(w, http.StatusGone, "enroll_token_exhausted", "enrollment token has no remaining uses")
		return
	}

	node, err := s.nodes.Get(r.Context(), output.NodeID, nil)
	if err != nil {
		s.internalError(w, err)
		return
	}
	nodeName := strings.TrimSpace(node.Name)
	if nodeName == "" {
		nodeName = hostname
	}
	created, err := s.apiTokens.Create(r.Context(), node.TenantID, service.APITokenInput{
		Name:   "client:" + nodeName,
		Scopes: []string{auth.ScopeNodesRead},
	})
	if err != nil {
		s.serviceError(w, err)
		return
	}

	desktop := s.remoteDesktop.Config(r.Context())
	apiServer := s.enrollURL
	if apiServer == "" {
		apiServer = output.Server
	}
	middleware.SetAuditAction(r.Context(), "client.enroll", "/api/v1/client/enroll", map[string]any{
		"result":       "success",
		"node_id":      node.ID,
		"token_prefix": auth.RedactAPIToken(created.Token),
	})
	writeJSON(w, http.StatusOK, clientEnrollOutput{
		NodeID:   node.ID.String(),
		NodeName: nodeName,
		Server: clientEnrollServer{
			IDServer:    desktop.IDServer,
			RelayServer: desktop.RelayServer,
			APIServer:   apiServer,
			Key:         desktop.PublicKey,
		},
		APIToken: created.Token,
	})
}
