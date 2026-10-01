package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/models"
)

type apiTokenResponse struct {
	Token    string `json:"token"`
	Notice   string `json:"notice"`
	APIToken struct {
		ID          string     `json:"id"`
		Name        string     `json:"name"`
		TokenPrefix string     `json:"token_prefix"`
		Scopes      []string   `json:"scopes"`
		ExpiresAt   *time.Time `json:"expires_at"`
		LastUsedAt  *time.Time `json:"last_used_at"`
		RevokedAt   *time.Time `json:"revoked_at"`
	} `json:"api_token"`
}

func TestAPITokenLifecycleScopesAuditAndTenantIsolation(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	created := createAPIToken(t, app, admin.Token, map[string]any{
		"name": "automation-read", "scopes": []string{auth.ScopeNodesRead}, "expires_in_days": 30,
	})
	if !strings.HasPrefix(created.Token, auth.APITokenPrefix) ||
		created.APIToken.TokenPrefix != created.Token[:8] ||
		!strings.Contains(created.Notice, "只显示一次") {
		t.Fatal("API token creation response metadata is incomplete")
	}
	var stored models.APIToken
	if err := app.db.First(&stored, "id = ?", created.APIToken.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == created.Token || stored.TokenHash != auth.HashAPIToken(created.Token) || stored.TokenPrefix != created.Token[:8] {
		t.Fatal("API token was not hash-only or prefix was incorrect")
	}

	status, _ := mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", created.Token, nil)
	requireStatus(t, status, http.StatusOK)
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", created.Token, map[string]any{
		"name": "must-not-register", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "insufficient_scope")
	if strings.Contains(string(body), created.Token) {
		t.Fatal("scope error leaked API token")
	}

	status, body = mustRequest(t, app.server, http.MethodDelete, "/api/v1/api-tokens/"+created.APIToken.ID, admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	status, body = mustRequest(t, app.server, http.MethodDelete, "/api/v1/api-tokens/"+created.APIToken.ID, admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var revokeResponse struct {
		AlreadyRevoked bool `json:"already_revoked"`
	}
	decodeResponse(t, body, &revokeResponse)
	if !revokeResponse.AlreadyRevoked {
		t.Fatal("revoke was not idempotent")
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", created.Token, nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "token_revoked")

	rotateSource := createAPIToken(t, app, admin.Token, map[string]any{
		"name": "rotate-me", "scopes": []string{auth.ScopeNodesRead},
	})
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/api-tokens/"+rotateSource.APIToken.ID+"/rotate", admin.Token, nil)
	requireStatus(t, status, http.StatusCreated)
	var rotated apiTokenResponse
	decodeSecretResponse(t, body, &rotated)
	if rotated.Token == "" || rotated.Token == rotateSource.Token || rotated.APIToken.ID == rotateSource.APIToken.ID {
		t.Fatal("rotation did not issue a distinct token")
	}
	status, _ = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", rotated.Token, nil)
	requireStatus(t, status, http.StatusOK)
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", rotateSource.Token, nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "token_revoked")

	limitedManager := createAPIToken(t, app, admin.Token, map[string]any{
		"name": "limited-token-manager", "scopes": []string{auth.ScopeNodesRead, auth.ScopeTokensWrite},
	})
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/api-tokens", limitedManager.Token, map[string]any{
		"name": "self-escalated", "scopes": []string{auth.ScopeAdmin},
	})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "insufficient_scope")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "other-api-tokens", "plan": "free"})
	requireStatus(t, status, http.StatusCreated)
	var otherTenant models.Tenant
	decodeResponse(t, body, &otherTenant)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID, "email": "other-token-admin@example.test",
		"password": "other-token-password", "role": auth.RoleTenantAdmin,
	})
	requireStatus(t, status, http.StatusCreated)
	otherAdmin := mustLogin(t, app, "other-token-admin@example.test", "other-token-password")
	otherToken := createAPIToken(t, app, otherAdmin.Token, map[string]any{
		"name": "other-tenant", "scopes": []string{auth.ScopeNetworksRead, auth.ScopeNetworksWrite},
	})

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", admin.Token, map[string]any{
		"name": "owned-network", "cidr": "10.90.0.0/24",
	})
	requireStatus(t, status, http.StatusCreated)
	var network models.VirtualNetwork
	decodeResponse(t, body, &network)
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/networks/"+network.ID.String(), otherToken.Token, nil)
	requireStatus(t, status, http.StatusNotFound)
	if strings.Contains(string(body), "owned-network") {
		t.Fatal("cross-tenant API token leaked network details")
	}
	status, _ = mustRequest(t, app.server, http.MethodPut, "/api/v1/networks/"+network.ID.String(), otherToken.Token, map[string]any{
		"name": "cross-tenant-write", "cidr": "10.91.0.0/24",
	})
	requireStatus(t, status, http.StatusNotFound)

	var audits []models.AuditLog
	if err := app.db.Where("action IN ?", []string{"api_token.create", "api_token.revoke", "api_token.rotate"}).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) < 5 {
		t.Fatalf("API token audit records = %d, want at least 5", len(audits))
	}
	for _, audit := range audits {
		text := string(audit.Detail)
		for _, secret := range []string{created.Token, rotateSource.Token, rotated.Token, limitedManager.Token, otherToken.Token} {
			if strings.Contains(text, secret) {
				t.Fatal("audit detail leaked a complete API token")
			}
		}
	}
}

func TestAPITokenExpiryInvalidAndLastUsedStates(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	created := createAPIToken(t, app, admin.Token, map[string]any{
		"name": "expiring", "scopes": []string{auth.ScopeNodesRead},
	})
	past := time.Now().UTC().Add(-time.Minute)
	if err := app.db.Model(&models.APIToken{}).Where("id = ?", created.APIToken.ID).Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", created.Token, nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "token_expired")
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", "umpp_not-a-real-token", nil)
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_token")
}

func createAPIToken(t *testing.T, app testApp, managerToken string, input map[string]any) apiTokenResponse {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/api-tokens", managerToken, input)
	requireStatus(t, status, http.StatusCreated)
	var response apiTokenResponse
	decodeSecretResponse(t, body, &response)
	return response
}

func decodeSecretResponse(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode secure response: %v", err)
	}
}
