package auth

import "sort"

const (
	ScopeNodesRead     = "nodes:read"
	ScopeNodesWrite    = "nodes:write"
	ScopeNetworksRead  = "networks:read"
	ScopeNetworksWrite = "networks:write"
	ScopeProxyRead     = "proxy:read"
	ScopeProxyWrite    = "proxy:write"
	ScopeCertsRead     = "certs:read"
	ScopeCertsWrite    = "certs:write"
	ScopeTokensRead    = "tokens:read"
	ScopeTokensWrite   = "tokens:write"
	ScopeAlertsRead    = "alerts:read"
	ScopeAlertsWrite   = "alerts:write"
	ScopeAdmin         = "admin"
)

// RoleScopes is the compatibility mapping from the existing dashboard RBAC
// roles to API-token scopes. API tokens use their own stored scopes strictly.
var RoleScopes = map[string][]string{
	RolePlatformAdmin: {ScopeAdmin},
	RoleTenantAdmin: {
		ScopeNodesRead, ScopeNodesWrite,
		ScopeNetworksRead, ScopeNetworksWrite,
		ScopeProxyRead, ScopeProxyWrite,
		ScopeCertsRead, ScopeCertsWrite,
		ScopeTokensRead, ScopeTokensWrite,
		ScopeAlertsRead, ScopeAlertsWrite,
	},
	RoleOps: {
		ScopeNodesRead, ScopeNodesWrite,
		ScopeNetworksRead, ScopeNetworksWrite,
		ScopeProxyRead, ScopeProxyWrite,
		ScopeCertsRead, ScopeCertsWrite,
		ScopeTokensRead,
		ScopeAlertsRead, ScopeAlertsWrite,
	},
	RoleReadonly: {
		ScopeNodesRead,
		ScopeNetworksRead,
		ScopeProxyRead,
		ScopeCertsRead,
		ScopeTokensRead,
		ScopeAlertsRead,
	},
}

var validScopes = map[string]struct{}{
	ScopeNodesRead: {}, ScopeNodesWrite: {},
	ScopeNetworksRead: {}, ScopeNetworksWrite: {},
	ScopeProxyRead: {}, ScopeProxyWrite: {},
	ScopeCertsRead: {}, ScopeCertsWrite: {},
	ScopeTokensRead: {}, ScopeTokensWrite: {},
	ScopeAlertsRead: {}, ScopeAlertsWrite: {},
	ScopeAdmin: {},
}

func ScopesForRole(role string) []string {
	values := append([]string(nil), RoleScopes[role]...)
	sort.Strings(values)
	return values
}

func ValidScope(scope string) bool {
	_, ok := validScopes[scope]
	return ok
}

func NormalizeScopes(scopes []string) ([]string, error) {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if !ValidScope(scope) {
			return nil, ErrInvalidScope
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	sort.Strings(result)
	return result, nil
}

func HasScope(granted []string, required ...string) bool {
	admin := false
	set := make(map[string]struct{}, len(granted))
	for _, scope := range granted {
		if scope == ScopeAdmin {
			admin = true
		}
		set[scope] = struct{}{}
	}
	for _, scope := range required {
		if admin {
			continue
		}
		if _, ok := set[scope]; !ok {
			return false
		}
	}
	return true
}

func MissingScopes(granted []string, required []string) []string {
	if HasScope(granted, required...) {
		return nil
	}
	missing := make([]string, 0, len(required))
	for _, scope := range required {
		if !HasScope(granted, scope) {
			missing = append(missing, scope)
		}
	}
	sort.Strings(missing)
	return missing
}

func CoversScopes(granted, requested []string) bool {
	return HasScope(granted, requested...)
}
