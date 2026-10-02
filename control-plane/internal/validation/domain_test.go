package validation

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"neilico/control-plane/internal/models"
)

func TestDomain(t *testing.T) {
	t.Parallel()
	valid := []string{"app.example.com", "example.com", "a-b.example", "UPPER.EXAMPLE.COM"}
	for _, input := range valid {
		got, err := Domain(input)
		if err != nil {
			t.Fatalf("Domain(%q) error = %v", input, err)
		}
		if got != strings.ToLower(input) {
			t.Fatalf("Domain(%q) = %q", input, got)
		}
	}
	invalid := []string{"", "*.example.com", "bad_domain.example", "-bad.example", "bad-.example", "a..example", "example.com.", "example.com?x=1"}
	for _, input := range invalid {
		if got, err := Domain(input); err == nil {
			t.Fatalf("Domain(%q) = %q, want error", input, got)
		}
	}
}

func TestProxyPathAndTargetValidation(t *testing.T) {
	t.Parallel()
	if got, err := ProxyPath(""); err != nil || got != "/" {
		t.Fatalf("ProxyPath(empty) = %q, %v", got, err)
	}
	for _, path := range []string{"api", "/bad path", "/x?y=1"} {
		if _, err := ProxyPath(path); err == nil {
			t.Fatalf("ProxyPath(%q) expected error", path)
		}
	}
	validTargets := map[string]string{
		TargetInternalIP: "192.168.1.10:8080",
		TargetVirtualIP:  "100.64.0.10:8080",
		TargetNode:       "3e5d90f2-6f1b-4a04-a48c-24f363d3d387:8080",
	}
	for targetType, target := range validTargets {
		if _, _, err := Target(targetType, target); err != nil {
			t.Fatalf("Target(%q, %q) error = %v", targetType, target, err)
		}
	}
	invalidTargets := []struct {
		kind   string
		target string
	}{
		{"unknown", "127.0.0.1:80"},
		{TargetInternalIP, "127.0.0.1"},
		{TargetInternalIP, "localhost:80"},
		{TargetInternalIP, "127.0.0.1:70000"},
		{TargetNode, "not-a-uuid:80"},
	}
	for _, item := range invalidTargets {
		if _, _, err := Target(item.kind, item.target); err == nil {
			t.Fatalf("Target(%q, %q) expected error", item.kind, item.target)
		}
	}
}

func TestAccessControlValidation(t *testing.T) {
	t.Parallel()
	if _, err := AccessControl(models.AccessControl{IPWhitelist: []string{"1.2.3.4/33"}}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
	if _, err := AccessControl(models.AccessControl{IPWhitelist: []string{"1.2.3.4/32", "2001:db8::1"}}); err != nil {
		t.Fatalf("valid whitelist rejected: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	value := models.AccessControl{BasicAuth: models.BasicAuth{Enabled: true, Username: "u", PasswordHash: string(hash)}}
	if _, err := AccessControl(value); err != nil {
		t.Fatalf("valid basic auth rejected: %v", err)
	}
	value.BasicAuth.PasswordHash = "not-bcrypt"
	if _, err := AccessControl(value); err == nil {
		t.Fatal("expected invalid bcrypt error")
	}
}
