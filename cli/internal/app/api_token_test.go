package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAPIToken = "neilico_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestLoginWithAPITokenSavesSecureCredentialWithoutEcho(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var stdout, stderr bytes.Buffer
	app := New(strings.NewReader(""), &stdout, &stderr)
	err := app.Run(context.Background(), []string{
		"--server", "https://api.example.test", "--config", path,
		"login", "--token", testAPIToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), testAPIToken) || strings.Contains(stderr.String(), testAPIToken) {
		t.Fatal("login output leaked API token")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(testAPIToken)) {
		t.Fatal("credential file omitted API token")
	}
}

func TestTokenCommandsListCreateAndRevoke(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/api-tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{
					"id": "11111111-1111-1111-1111-111111111111", "name": "ci",
					"token_prefix": "neilico_AAA", "scopes": []string{"nodes:read"},
				}},
				"total": 1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/api-tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": testAPIToken, "notice": "此 token 只显示一次",
				"api_token": map[string]any{"id": "22222222-2222-2222-2222-222222222222", "token_prefix": "neilico_AAA"},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/api-tokens/22222222-2222-2222-2222-222222222222":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"api_token":       map[string]any{"id": "22222222-2222-2222-2222-222222222222", "token_prefix": "neilico_AAA"},
				"already_revoked": false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	run := func(args ...string) (string, error) {
		var stdout, stderr bytes.Buffer
		err := New(strings.NewReader(""), &stdout, &stderr).Run(context.Background(), args)
		if strings.Contains(stderr.String(), testAPIToken) {
			t.Fatal("command stderr leaked API token")
		}
		return stdout.String(), err
	}
	out, err := run("--server", server.URL, "--config", filepath.Join(t.TempDir(), "config.yaml"), "token", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "neilico_AAA\u2026") || strings.Contains(out, testAPIToken) {
		t.Fatal("token list was not masked")
	}

	out, err = run("--server", server.URL, "--config", filepath.Join(t.TempDir(), "config.yaml"), "token", "create",
		"--name=ci", "--scopes=nodes:read,tokens:write", "--expires-in-days=30")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, testAPIToken) != 1 || !strings.Contains(out, "只显示一次") {
		t.Fatal("token create did not print exactly one one-time credential")
	}

	out, err = run("--server", server.URL, "--config", filepath.Join(t.TempDir(), "config.yaml"), "token", "revoke",
		"--id=22222222-2222-2222-2222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, testAPIToken) || !strings.Contains(out, "neilico_AAA\u2026") {
		t.Fatal("token revoke output leaked credential or omitted mask")
	}
}
