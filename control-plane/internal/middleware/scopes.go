package middleware

import (
	"context"
	"net/http"
	"strings"

	"umpp/control-plane/internal/auth"
)

type scopePolicy struct {
	prefix string
	exact  bool
	read   string
	write  string
}

var scopePolicies = []scopePolicy{
	{prefix: "/api/v1/users", read: auth.ScopeAdmin, write: auth.ScopeAdmin},
	{prefix: "/api/v1/tenants", read: auth.ScopeAdmin, write: auth.ScopeAdmin},
	{prefix: "/api/v1/audit-logs", exact: true, read: auth.ScopeAdmin, write: auth.ScopeAdmin},
	{prefix: "/api/v1/relay-servers", read: auth.ScopeAdmin, write: auth.ScopeAdmin},
	{prefix: "/api/v1/api-tokens", read: auth.ScopeTokensRead, write: auth.ScopeTokensWrite},
	{prefix: "/api/v1/alerts", read: auth.ScopeAlertsRead, write: auth.ScopeAlertsWrite},
	{prefix: "/api/v1/certificates", read: auth.ScopeCertsRead, write: auth.ScopeCertsWrite},
	{prefix: "/api/v1/domains", read: auth.ScopeProxyRead, write: auth.ScopeProxyWrite},
	{prefix: "/api/v1/proxy-rules", read: auth.ScopeProxyRead, write: auth.ScopeProxyWrite},
	{prefix: "/api/v1/proxy/providers", exact: true, read: auth.ScopeProxyRead, write: auth.ScopeProxyWrite},
	{prefix: "/api/v1/proxy/render", exact: true, read: auth.ScopeProxyRead, write: auth.ScopeProxyRead},
	{prefix: "/api/v1/networks", read: auth.ScopeNetworksRead, write: auth.ScopeNetworksWrite},
	{prefix: "/api/v1/configs", read: auth.ScopeNetworksRead, write: auth.ScopeNetworksWrite},
	{prefix: "/api/v1/nodes", read: auth.ScopeNodesRead, write: auth.ScopeNodesWrite},
	{prefix: "/api/v1/traffic", exact: true, read: auth.ScopeNodesRead, write: auth.ScopeNodesWrite},
}

// RequireScope returns a middleware requiring every named scope. The admin
// scope is a wildcard. It must run after authentication.
func RequireScope(scopes ...string) func(http.Handler) http.Handler {
	required := append([]string(nil), scopes...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeUnauthorized(w)
				return
			}
			granted := principal.Scopes
			if principal.AuthMethod == auth.AuthMethodJWT {
				granted = auth.ScopesForRole(principal.Role)
			}
			if auth.HasScope(granted, required...) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), scopeAuthorizedKey, true)))
				return
			}
			writeInsufficientScope(w, principal.AuthMethod, auth.MissingScopes(granted, required))
		})
	}
}

// RequireScopeForRequest applies the API resource scope table based on method.
// Resources that have no V1 scope constant remain governed by their existing
// RBAC checks and are never usable by API tokens without a matching role.
func RequireScopeForRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required := ScopeForRequest(r.Method, r.URL.Path)
		if len(required) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		// Users/tenants/audit/relay keep their established RBAC semantics for
		// JWT users. API tokens need the admin wildcard for these unscoped V1
		// resources, preventing a narrow resource token from reading them.
		if principal, ok := PrincipalFromContext(r.Context()); ok &&
			principal.AuthMethod == auth.AuthMethodJWT && required[0] == auth.ScopeAdmin {
			next.ServeHTTP(w, r)
			return
		}
		RequireScope(required...)(next).ServeHTTP(w, r)
	})
}

func ScopeForRequest(method, path string) []string {
	for _, policy := range scopePolicies {
		matched := policy.exact && path == policy.prefix
		if !policy.exact && (path == policy.prefix || strings.HasPrefix(path, policy.prefix+"/")) {
			matched = true
		}
		if !matched {
			continue
		}
		if method == http.MethodGet || method == http.MethodHead {
			return []string{policy.read}
		}
		return []string{policy.write}
	}
	return nil
}

func writeInsufficientScope(w http.ResponseWriter, authMethod string, missing []string) {
	// Preserve the established JWT/RBAC response contract while API tokens use
	// the explicit machine-readable insufficient_scope error code.
	code := "insufficient_scope"
	message := "insufficient scope"
	if authMethod == auth.AuthMethodJWT {
		code = "forbidden"
		message = "insufficient role"
	}
	writeErrorDetail(w, http.StatusForbidden, code, message, map[string]any{
		"required_scope": strings.Join(missing, ","),
		"reason":         "insufficient_scope",
	})
}
