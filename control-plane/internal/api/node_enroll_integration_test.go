package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/models"
)

type enrollTokenCreateResponse struct {
	ID        uuid.UUID  `json:"id"`
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
	MaxUses   int        `json:"max_uses"`
	NetworkID *uuid.UUID `json:"network_id"`
	NameHint  string     `json:"name_hint"`
	Server    string     `json:"server"`
	Commands  struct {
		Linux  string `json:"linux"`
		Docker string `json:"docker"`
	} `json:"commands"`
}

type nodeEnrollResponse struct {
	NodeID     uuid.UUID  `json:"node_id"`
	AgentToken string     `json:"agent_token"`
	PrivateKey string     `json:"private_key"`
	PublicKey  string     `json:"public_key"`
	VirtualIP  string     `json:"virtual_ip"`
	Server     string     `json:"server"`
	NetworkID  *uuid.UUID `json:"network_id"`
	Replayed   bool       `json:"replayed"`
}

func TestNodeEnrollTokenLifecycleAndSelfEnroll(t *testing.T) {
	downloadDir := t.TempDir()
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		Downloads: api.DownloadsOptions{Dir: downloadDir},
	})
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", admin.Token, map[string]any{
		"name": "enroll", "cidr": "100.64.90.0/24",
	})
	requireStatus(t, status, http.StatusCreated)
	var network models.VirtualNetwork
	decodeResponse(t, body, &network)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", admin.Token, map[string]any{
		"network_id": network.ID, "name_hint": "edge", "expires_in_seconds": 3600, "max_uses": 1,
	})
	requireStatus(t, status, http.StatusCreated)
	var created enrollTokenCreateResponse
	decodeSecretResponse(t, body, &created)
	if created.Token == "" || created.Server != app.server.URL || created.NetworkID == nil || *created.NetworkID != network.ID {
		t.Fatalf("unexpected enroll token response: %#v", created)
	}
	if !strings.Contains(created.Commands.Linux, created.Token) || !strings.Contains(created.Commands.Docker, "NEILICO_TOKEN="+created.Token) {
		t.Fatalf("creation response omitted ready-to-run commands: %#v", created.Commands)
	}
	sum := sha256.Sum256([]byte(created.Token))
	var tokenRow models.NodeEnrollToken
	if err := app.db.First(&tokenRow, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if tokenRow.TokenHash != hex.EncodeToString(sum[:]) || tokenRow.TokenHash == created.Token {
		t.Fatal("enrollment token plaintext was not replaced by its SHA-256 hash")
	}

	enrollRequest := map[string]any{
		"token": created.Token, "name": "edge-01", "hostname": "edge-01",
		"os": "linux", "arch": "amd64", "version": "test", "tags": []string{"edge"},
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", enrollRequest)
	requireStatus(t, status, http.StatusOK)
	var enrolled nodeEnrollResponse
	decodeResponse(t, body, &enrolled)
	if enrolled.NodeID == uuid.Nil || enrolled.AgentToken == "" || enrolled.PrivateKey == "" ||
		enrolled.VirtualIP == "" || enrolled.NetworkID == nil || *enrolled.NetworkID != network.ID {
		t.Fatalf("unexpected enrollment response: %#v", enrolled)
	}
	if enrolled.Server != app.server.URL {
		t.Fatalf("enrollment server = %q, want %q", enrolled.Server, app.server.URL)
	}

	var member models.NetworkMember
	if err := app.db.First(&member, "network_id = ? AND node_id = ?", network.ID, enrolled.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if member.VirtualIP != enrolled.VirtualIP {
		t.Fatalf("allocated VIP = %q, response VIP = %q", member.VirtualIP, enrolled.VirtualIP)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", enrollRequest)
	requireStatus(t, status, http.StatusOK)
	var replay nodeEnrollResponse
	decodeResponse(t, body, &replay)
	if replay.NodeID != enrolled.NodeID || !replay.Replayed || replay.AgentToken != "" || replay.PrivateKey != "" {
		t.Fatalf("unexpected replay response: %#v", replay)
	}
	var nodeCount int64
	if err := app.db.Model(&models.Node{}).Where("tenant_id = ?", tokenRow.TenantID).Count(&nodeCount).Error; err != nil {
		t.Fatal(err)
	}
	if nodeCount != 1 {
		t.Fatalf("node count after replay = %d, want 1", nodeCount)
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": created.Token, "name": "edge-02", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusGone)
	requireErrorCode(t, body, "enroll_token_exhausted")

	var audits []models.AuditLog
	if err := app.db.Where("action = ?", "node.enroll").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) < 3 {
		t.Fatalf("node.enroll audit count = %d, want at least 3", len(audits))
	}
	for _, audit := range audits {
		text := string(audit.Detail)
		if strings.Contains(text, created.Token) || strings.Contains(text, enrolled.AgentToken) || strings.Contains(text, enrolled.PrivateKey) {
			t.Fatal("enrollment audit leaked a one-time secret")
		}
	}
	foundAuditIDs := false
	for _, audit := range audits {
		text := string(audit.Detail)
		if strings.Contains(text, created.ID.String()) && strings.Contains(text, enrolled.NodeID.String()) {
			foundAuditIDs = true
		}
	}
	if !foundAuditIDs {
		t.Fatal("enrollment audit omitted token/node IDs")
	}

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", admin.Token, map[string]any{
		"expires_in_seconds": 3600, "max_uses": 1,
	})
	requireStatus(t, status, http.StatusCreated)
	decodeSecretResponse(t, body, &created)
	status, _ = mustRequest(t, app.server, http.MethodDelete, "/api/v1/enroll-tokens/"+created.ID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusNoContent)
	status, _ = mustRequest(t, app.server, http.MethodDelete, "/api/v1/enroll-tokens/"+created.ID.String(), admin.Token, nil)
	requireStatus(t, status, http.StatusNoContent)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": created.Token, "name": "revoked", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_enroll_token")

	tampered := created.Token[:len(created.Token)-1] + "A"
	if tampered == created.Token {
		tampered = created.Token[:len(created.Token)-1] + "B"
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": tampered, "name": "tampered", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_enroll_token")

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", admin.Token, map[string]any{
		"expires_in_seconds": 3600, "max_uses": 1,
	})
	requireStatus(t, status, http.StatusCreated)
	decodeSecretResponse(t, body, &created)
	past := time.Now().UTC().Add(-time.Minute)
	if err := app.db.Model(&models.NodeEnrollToken{}).Where("id = ?", created.ID).
		Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": created.Token, "name": "expired", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_enroll_token")

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/enroll-tokens", admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	var list struct {
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	decodeResponse(t, body, &list)
	if len(list.Items) < 3 {
		t.Fatalf("enrollment token list length = %d, want at least 3", len(list.Items))
	}
}

func TestEnrollTokenCannotTargetAnotherTenantNetwork(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/tenants", admin.Token, map[string]any{"name": "other-enroll", "plan": "free"})
	requireStatus(t, status, http.StatusCreated)
	var otherTenant models.Tenant
	decodeResponse(t, body, &otherTenant)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID, "email": "other-enroll@example.test", "password": "other-enroll-password", "role": "tenant_admin",
	})
	requireStatus(t, status, http.StatusCreated)
	otherLogin := mustLogin(t, app, "other-enroll@example.test", "other-enroll-password")
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/networks", otherLogin.Token, map[string]any{
		"name": "other-network", "cidr": "100.64.91.0/24",
	})
	requireStatus(t, status, http.StatusCreated)
	var otherNetwork models.VirtualNetwork
	decodeResponse(t, body, &otherNetwork)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", admin.Token, map[string]any{
		"network_id": otherNetwork.ID, "expires_in_seconds": 3600,
	})
	requireStatus(t, status, http.StatusNotFound)

	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", otherLogin.Token, map[string]any{
		"network_id": otherNetwork.ID, "expires_in_seconds": 3600,
	})
	requireStatus(t, status, http.StatusCreated)
	var created enrollTokenCreateResponse
	decodeSecretResponse(t, body, &created)
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": created.Token, "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusOK)
	var enrolled nodeEnrollResponse
	decodeResponse(t, body, &enrolled)
	var node models.Node
	if err := app.db.First(&node, "id = ?", enrolled.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if node.TenantID != otherTenant.ID {
		t.Fatalf("node tenant = %s, want %s", node.TenantID, otherTenant.ID)
	}
}

func TestInstallScriptAndDownloadEndpoint(t *testing.T) {
	downloadDir := t.TempDir()
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		Downloads: api.DownloadsOptions{Dir: downloadDir},
	})
	defer app.server.Close()

	status, body := mustRequest(t, app.server, http.MethodGet, "/install.sh", "", nil)
	requireStatus(t, status, http.StatusOK)
	script := string(body)
	if !strings.Contains(script, "set -eu") || !strings.Contains(script, "--dry-run") ||
		!strings.Contains(script, "X-Neilico-Sha256") || !strings.Contains(script, "systemctl enable --now neilico-agent") {
		t.Fatalf("install script omitted required behavior:\n%s", script)
	}
	scriptPath := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(scriptPath, body, 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, output)
	}
	output, err := exec.Command("bash", scriptPath, "--dry-run", "--server", app.server.URL, "--token", "neilico-enroll.test.test").CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	for _, expected := range []string{"DRY-RUN", "would download", "would verify", "would write", "systemctl restart"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("dry-run omitted %q:\n%s", expected, output)
		}
	}
	status, body = mustRequest(t, app.server, http.MethodGet, "/downloads/neilico-agent-linux-amd64", "", nil)
	requireStatus(t, status, http.StatusNotFound)
	if strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatalf("missing download returned HTML: %s", body)
	}

	content := []byte("fake-agent-binary")
	if err := os.WriteFile(filepath.Join(downloadDir, "neilico-agent-linux-amd64"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, app.server.URL+"/downloads/neilico-agent-linux-amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d", response.StatusCode)
	}
	sum := sha256.Sum256(content)
	if got := response.Header.Get("X-Neilico-Sha256"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("download checksum header = %q, want %q", got, hex.EncodeToString(sum[:]))
	}
	status, _ = mustRequest(t, app.server, http.MethodGet, "/downloads/neilico-agent-windows-amd64", "", nil)
	requireStatus(t, status, http.StatusNotFound)
}

func TestEnrollEndpointUsesSourceIPRateLimit(t *testing.T) {
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		RateLimit: api.RateLimitOptions{Enabled: true, RPS: 0.001, Burst: 1},
	})
	defer app.server.Close()
	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": "bad", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusUnauthorized)
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/enroll", "", map[string]any{
		"token": "bad", "os": "linux", "arch": "amd64", "version": "test",
	})
	requireStatus(t, status, http.StatusTooManyRequests)
	requireErrorCode(t, body, "rate_limited")
}
