package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/middleware"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
	"neilico/control-plane/pkg/enrolltoken"
)

type enrollTokenView struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	NetworkID *uuid.UUID `json:"network_id,omitempty"`
	NameHint  string     `json:"name_hint"`
	ExpiresAt time.Time  `json:"expires_at"`
	MaxUses   int        `json:"max_uses"`
	UsedCount int        `json:"used_count"`
	Status    string     `json:"status"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type enrollCommands struct {
	Linux   string `json:"linux"`
	MacOS   string `json:"macos"`
	Windows string `json:"windows"`
	Docker  string `json:"docker"`
}

func (s *Server) registerEnroll(mux *http.ServeMux) {
	mux.Handle("/api/v1/enroll-tokens", s.authed(http.HandlerFunc(s.handleEnrollTokens)))
	mux.Handle("/api/v1/enroll-tokens/{id}", s.authed(http.HandlerFunc(s.handleEnrollTokenItem)))
	mux.Handle("/api/v1/nodes/enroll", middleware.RateLimit(s.rateLimiter, http.HandlerFunc(s.handleNodeEnroll)))
}

func (s *Server) handleEnrollTokens(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.enrollTokens.List(r.Context(), s.userScope(principal))
		if err != nil {
			s.internalError(w, err)
			return
		}
		views := make([]enrollTokenView, 0, len(items))
		now := time.Now().UTC()
		for _, item := range items {
			views = append(views, newEnrollTokenView(item, now))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": views, "total": len(views)})
	case http.MethodPost:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps) {
			writeError(w, http.StatusForbidden, "forbidden", "nodes:write role required")
			return
		}
		var input service.NodeEnrollTokenInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		server, err := s.enrollServerURL(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.enrollTokens.Create(r.Context(), principal.TenantID, enrollCreatedBy(principal), input, server)
		if err != nil {
			s.serviceError(w, err)
			return
		}
		middleware.SetAuditAction(r.Context(), "enroll_token.create", "/api/v1/enroll-tokens", map[string]any{
			"enroll_token_id": created.Item.ID,
			"network_id":      created.Item.NetworkID,
			"name_hint":       created.Item.NameHint,
		})
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":         created.Item.ID,
			"token":      created.Token,
			// 命令是在此刻由后端固化的：前端据此提示"升级/改配置后需重新生成"
			"created_at": created.Item.CreatedAt,
			"expires_at": created.Item.ExpiresAt,
			"max_uses":   created.Item.MaxUses,
			"used_count": created.Item.UsedCount,
			"network_id": created.Item.NetworkID,
			"name_hint":  created.Item.NameHint,
			"server":     server,
			"commands":   enrollCommandSet(server, created.Token, s.agentImage),
			// 让界面能明确标出 Docker 命令的镜像来源（用户看不出隐式拉取）
			"agent_image": s.agentImage,
		})
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleEnrollTokenItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	if r.Method != http.MethodDelete {
		s.methodNotAllowed(w, http.MethodDelete)
		return
	}
	if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps) {
		writeError(w, http.StatusForbidden, "forbidden", "nodes:write role required")
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "enrollment token ID")
	if !ok {
		return
	}
	item, alreadyRevoked, err := s.enrollTokens.Revoke(r.Context(), s.userScope(principal), id)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	middleware.SetAuditAction(r.Context(), "enroll_token.revoke", "/api/v1/enroll-tokens/"+id.String(), map[string]any{
		"enroll_token_id": item.ID,
		"already_revoked": alreadyRevoked,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNodeEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	var input service.NodeEnrollInput
	if !s.decodeRequest(w, r, &input) {
		middleware.SetAuditAction(r.Context(), "node.enroll", "/api/v1/nodes/enroll", map[string]any{"result": "invalid_request"})
		return
	}
	output, err := s.enrollNodes.Enroll(r.Context(), input.Token, input)
	if err != nil {
		result := "unauthorized"
		status := http.StatusUnauthorized
		code := "invalid_enroll_token"
		message := "invalid enrollment token"
		switch {
		case errors.Is(err, service.ErrEnrollGone):
			result, status, code, message = "exhausted", http.StatusGone, "enroll_token_exhausted", "enrollment token has no remaining uses"
		case errors.Is(err, service.ErrInvalidInput):
			result, status, code, message = "invalid_request", http.StatusBadRequest, "invalid_request", err.Error()
		}
		middleware.SetAuditAction(r.Context(), "node.enroll", "/api/v1/nodes/enroll", map[string]any{"result": result})
		writeError(w, status, code, message)
		return
	}
	middleware.SetAuditAction(r.Context(), "node.enroll", "/api/v1/nodes/enroll", map[string]any{
		"result":          "success",
		"enroll_token_id": outputTokenID(input.Token),
		"jti":             outputTokenID(input.Token),
		"node_id":         output.NodeID,
		"network_id":      output.NetworkID,
		"replayed":        output.Replayed,
	})
	writeJSON(w, http.StatusOK, output)
}

func newEnrollTokenView(item models.NodeEnrollToken, now time.Time) enrollTokenView {
	return enrollTokenView{
		ID: item.ID, TenantID: item.TenantID, NetworkID: item.NetworkID,
		NameHint: item.NameHint, ExpiresAt: item.ExpiresAt, MaxUses: item.MaxUses,
		UsedCount: item.UsedCount, Status: service.NodeEnrollTokenStatus(item, now),
		RevokedAt: item.RevokedAt, CreatedBy: item.CreatedBy, CreatedAt: item.CreatedAt,
	}
}

func enrollCreatedBy(principal middleware.Principal) *uuid.UUID {
	if principal.UserID == uuid.Nil {
		return nil
	}
	id := principal.UserID
	return &id
}

func (s *Server) enrollServerURL(r *http.Request) (string, error) {
	if s.enrollURL != "" {
		if err := validateEnrollServer(s.enrollURL); err != nil {
			return "", err
		}
		return s.enrollURL, nil
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	if strings.TrimSpace(r.Host) == "" {
		return "", errors.New("unable to determine public server URL; set enroll.public_url")
	}
	value := scheme + "://" + strings.TrimSpace(r.Host)
	if err := validateEnrollServer(value); err != nil {
		return "", err
	}
	return value, nil
}

func validateEnrollServer(value string) error {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil {
		return errors.New("enroll.public_url must be an absolute http or https URL")
	}
	return nil
}

func enrollCommandSet(server, token, image string) enrollCommands {
	base := strings.TrimRight(server, "/")
	shellCommand := "curl -fsSL " + base + "/install.sh | sudo bash -s -- --token " + token

	// Docker：直接从镜像仓库拉官方 agent 镜像。
	//   早先这里写的是本机 tag `neilico-agent:local` —— 那玩意儿只在本机构建过，
	//   别的机器 `docker pull neilico-agent:local` 会得到
	//     pull access denied for neilico-agent, repository does not exist
	//   （实测）。现改为可配置的镜像地址（默认 ghcr.io，见 config.DefaultAgentImage）。
	// 行尾用「空格 + 反斜杠 + 换行」续行，整段可直接粘进 shell。
	dockerLines := []string{
		"docker run -d --name neilico-agent --restart unless-stopped",
		"  --network host --cap-add NET_ADMIN --device /dev/net/tun",
		"  -v neilico-agent-state:/var/lib/neilico-agent",
		"  -e NEILICO_TOKEN=" + token + " " + image,
	}
	// 先显式 `docker pull` 再 run。
	//   为什么要写出来：`docker run` 是**隐式**拉取，命令末尾那串镜像地址用户根本看不出
	//   "这是从 GitHub 拉的"。写成两步后，来源一目了然；也便于单独排查拉取失败。
	dockerCommand := "docker pull " + image + " && \\\n" + strings.Join(dockerLines, " \\\n")

	return enrollCommands{
		Linux:   shellCommand,
		MacOS:   shellCommand,
		Windows: `powershell -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm ` + base + `/install.ps1))) -Token ` + token + `"`,
		Docker:  dockerCommand,
	}
}

func outputTokenID(token string) uuid.UUID {
	payload, err := enrolltoken.Inspect(token)
	if err != nil {
		return uuid.Nil
	}
	return payload.TokenID
}

func enrollSigningKey(manager *auth.Manager, configured string) []byte {
	if strings.TrimSpace(configured) != "" {
		return []byte(configured)
	}
	return manager.EnrollSigningKey()
}
