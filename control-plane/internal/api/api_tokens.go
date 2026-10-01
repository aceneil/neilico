package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/middleware"
	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service"
)

type apiTokenView struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type apiTokenCreateInput struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays *int     `json:"expires_in_days,omitempty"`
}

const apiTokenOneTimeNotice = "此 token 只显示一次，请立即安全保存；服务端仅保存 SHA-256 哈希。"

func (s *Server) registerAPITokens(mux *http.ServeMux) {
	mux.Handle("/api/v1/api-tokens", s.authed(http.HandlerFunc(s.handleAPITokens)))
	mux.Handle("/api/v1/api-tokens/{id}", s.authed(http.HandlerFunc(s.handleAPITokenItem)))
	mux.Handle("/api/v1/api-tokens/{id}/rotate", s.authed(http.HandlerFunc(s.handleAPITokenRotate)))
}

func (s *Server) handleAPITokens(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin, auth.RoleOps, auth.RoleReadonly) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		items, err := s.apiTokens.List(r.Context(), s.tokenTenantScope(principal))
		if err != nil {
			s.internalError(w, err)
			return
		}
		views := make([]apiTokenView, 0, len(items))
		for _, item := range items {
			views = append(views, newAPITokenView(item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": views, "total": len(views)})
	case http.MethodPost:
		if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
			writeError(w, http.StatusForbidden, "forbidden", "tokens:write role required")
			return
		}
		var input apiTokenCreateInput
		if !s.decodeRequest(w, r, &input) {
			return
		}
		if principal.AuthMethod == auth.AuthMethodAPIToken &&
			!auth.CoversScopes(principal.Scopes, input.Scopes) {
			writeInsufficientScopes(w, auth.MissingScopes(principal.Scopes, input.Scopes))
			return
		}
		userID := (*uuid.UUID)(nil)
		if principal.UserID != uuid.Nil {
			id := principal.UserID
			userID = &id
		}
		created, err := s.apiTokens.Create(r.Context(), principal.TenantID, service.APITokenInput{
			Name: input.Name, Scopes: input.Scopes, ExpiresInDays: input.ExpiresInDays, UserID: userID,
		})
		if err != nil {
			s.serviceError(w, err)
			return
		}
		middleware.SetAuditAction(r.Context(), "api_token.create", "/api/v1/api-tokens", map[string]any{
			"token_prefix": auth.RedactAPIToken(created.Token),
			"name":         created.APIToken.Name,
			"scopes":       created.APIToken.Scopes,
		})
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":     created.Token,
			"notice":    apiTokenOneTimeNotice,
			"api_token": newAPITokenView(created.APIToken),
		})
	default:
		s.methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleAPITokenItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "API token ID")
	if !ok {
		return
	}
	if r.Method != http.MethodDelete {
		s.methodNotAllowed(w, http.MethodDelete)
		return
	}
	if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "tokens:write role required")
		return
	}
	item, alreadyRevoked, err := s.apiTokens.Revoke(r.Context(), s.tokenTenantScope(principal), id)
	if err != nil {
		s.serviceError(w, err)
		return
	}
	middleware.SetAuditAction(r.Context(), "api_token.revoke", "/api/v1/api-tokens/"+id.String(), map[string]any{
		"token_prefix":    auth.RedactAPIToken(item.TokenPrefix),
		"name":            item.Name,
		"already_revoked": alreadyRevoked,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"api_token":       newAPITokenView(item),
		"already_revoked": alreadyRevoked,
	})
}

func (s *Server) handleAPITokenRotate(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid access token required")
		return
	}
	id, ok := s.pathID(w, r.PathValue("id"), "API token ID")
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, http.MethodPost)
		return
	}
	if !roleAllowed(r.Context(), principal, auth.RolePlatformAdmin, auth.RoleTenantAdmin) {
		writeError(w, http.StatusForbidden, "forbidden", "tokens:write role required")
		return
	}
	if principal.AuthMethod == auth.AuthMethodAPIToken {
		old, getErr := s.apiTokens.Get(r.Context(), s.tokenTenantScope(principal), id)
		if getErr != nil {
			s.serviceError(w, getErr)
			return
		}
		if !auth.CoversScopes(principal.Scopes, old.Scopes) {
			writeInsufficientScopes(w, auth.MissingScopes(principal.Scopes, old.Scopes))
			return
		}
	}
	created, err := s.apiTokens.Rotate(r.Context(), s.tokenTenantScope(principal), id)
	if err != nil {
		if errors.Is(err, service.ErrTokenRevoked) {
			writeError(w, http.StatusConflict, "token_revoked", "revoked API token cannot be rotated")
			return
		}
		s.serviceError(w, err)
		return
	}
	middleware.SetAuditAction(r.Context(), "api_token.rotate", "/api/v1/api-tokens/"+id.String()+"/rotate", map[string]any{
		"old_token_prefix": auth.RedactAPIToken(created.APIToken.TokenPrefix),
		"new_token_prefix": auth.RedactAPIToken(created.Token),
		"name":             created.APIToken.Name,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     created.Token,
		"notice":    apiTokenOneTimeNotice,
		"api_token": newAPITokenView(created.APIToken),
	})
}

func (s *Server) tokenTenantScope(principal middleware.Principal) *uuid.UUID {
	if principal.AuthMethod == auth.AuthMethodAPIToken {
		tenantID := principal.TenantID
		return &tenantID
	}
	return s.userScope(principal)
}

func newAPITokenView(item models.APIToken) apiTokenView {
	return apiTokenView{
		ID: item.ID.String(), Name: item.Name, TokenPrefix: item.TokenPrefix,
		Scopes: append([]string(nil), item.Scopes...), ExpiresAt: item.ExpiresAt,
		LastUsedAt: item.LastUsedAt, RevokedAt: item.RevokedAt,
	}
}

func writeInsufficientScopes(w http.ResponseWriter, missing []string) {
	writeErrorDetail(w, http.StatusForbidden, "insufficient_scope", "insufficient scope", map[string]any{
		"required_scope": missing,
	})
}
