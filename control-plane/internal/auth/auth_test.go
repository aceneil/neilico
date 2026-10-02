package auth

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/models"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("password hash contains plaintext")
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Fatal("CheckPassword() rejected valid password")
	}
	if CheckPassword(hash, "wrong password") {
		t.Fatal("CheckPassword() accepted invalid password")
	}
}

func TestJWTSignValidateTamperAndExpiry(t *testing.T) {
	manager, err := NewManager("0123456789abcdef0123456789abcdef", time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{ID: uuid.New(), TenantID: uuid.New(), Role: RoleTenantAdmin}
	token, err := manager.IssueAccess(user)
	if err != nil {
		t.Fatalf("IssueAccess() error = %v", err)
	}
	claims, err := manager.Parse(token, TokenAccess)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.Subject != user.ID.String() || claims.UserID != user.ID || claims.TenantID != user.TenantID || claims.Role != user.Role {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("exp claim missing")
	}

	dot := strings.LastIndex(token, ".")
	replacement := "A"
	if token[dot+1] == replacement[0] {
		replacement = "B"
	}
	tampered := token[:dot+1] + replacement + token[dot+2:]
	if _, err := manager.Parse(tampered, TokenAccess); err == nil {
		t.Fatal("Parse() accepted tampered token")
	}

	expired, err := manager.issue(user, TokenAccess, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Parse(expired, TokenAccess); err == nil {
		t.Fatal("Parse() accepted expired token")
	}

	refresh, err := manager.IssueRefresh(user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Parse(refresh, TokenAccess); err == nil {
		t.Fatal("Parse() accepted refresh token as access token")
	}
	if _, err := manager.Parse(token, TokenRefresh); err == nil {
		t.Fatal("Parse() accepted access token as refresh token")
	}
}

func TestRBACDecisions(t *testing.T) {
	if !RoleAllowed(RoleTenantAdmin, RolePlatformAdmin, RoleTenantAdmin) {
		t.Fatal("tenant_admin should be allowed")
	}
	if RoleAllowed(RoleReadonly, RolePlatformAdmin, RoleTenantAdmin) {
		t.Fatal("readonly should not manage users")
	}
	if !CanManageUsers(RoleTenantAdmin) || CanManageUsers(RoleOps) {
		t.Fatal("unexpected CanManageUsers decision")
	}
	if !CanManageNodes(RoleOps) || CanManageNodes(RoleReadonly) {
		t.Fatal("unexpected CanManageNodes decision")
	}
}

func TestAgentTokenHash(t *testing.T) {
	plain, hash, err := GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if plain == "" || len(hash) != 64 || strings.Contains(hash, plain) {
		t.Fatal("invalid generated agent token")
	}
	if HashAgentToken(plain) != hash || !AgentTokenEqual(hash, plain) {
		t.Fatal("agent token hash mismatch")
	}
	if AgentTokenEqual(hash, plain+"forged") {
		t.Fatal("forged agent token matched")
	}
}

func TestAPITokenFormatHashAndScopeTables(t *testing.T) {
	plain, hash, prefix, err := GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "neilico_") || len(plain) != len("neilico_")+43 {
		t.Fatalf("unexpected API token format length=%d prefix=%t", len(plain), strings.HasPrefix(plain, "neilico_"))
	}
	if prefix != plain[:8] || len(hash) != 64 || strings.Contains(hash, plain) {
		t.Fatal("API token hash or prefix mismatch")
	}
	if HashAPIToken(plain) != hash || RedactAPIToken(plain) != prefix+"\u2026" {
		t.Fatal("API token hashing/redaction mismatch")
	}

	expected := map[string][]string{
		RolePlatformAdmin: {ScopeAdmin},
		RoleTenantAdmin: {
			ScopeNodesRead, ScopeNodesWrite, ScopeNetworksRead, ScopeNetworksWrite,
			ScopeProxyRead, ScopeProxyWrite, ScopeCertsRead, ScopeCertsWrite,
			ScopeTokensRead, ScopeTokensWrite, ScopeAlertsRead, ScopeAlertsWrite,
		},
		RoleOps: {
			ScopeNodesRead, ScopeNodesWrite, ScopeNetworksRead, ScopeNetworksWrite,
			ScopeProxyRead, ScopeProxyWrite, ScopeCertsRead, ScopeCertsWrite,
			ScopeTokensRead, ScopeAlertsRead, ScopeAlertsWrite,
		},
		RoleReadonly: {
			ScopeNodesRead, ScopeNetworksRead, ScopeProxyRead,
			ScopeCertsRead, ScopeTokensRead, ScopeAlertsRead,
		},
	}
	for role, want := range expected {
		got := ScopesForRole(role)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s scopes = %#v, want %#v", role, got, want)
		}
	}
	if !HasScope([]string{ScopeAdmin}, ScopeTokensWrite) {
		t.Fatal("admin scope is not wildcard")
	}
	if HasScope([]string{ScopeNodesRead}, ScopeNodesWrite) {
		t.Fatal("read scope unexpectedly granted write")
	}
	if missing := MissingScopes([]string{ScopeNodesRead}, []string{ScopeNodesRead, ScopeNodesWrite}); !reflect.DeepEqual(missing, []string{ScopeNodesWrite}) {
		t.Fatalf("missing scopes = %#v", missing)
	}
}
