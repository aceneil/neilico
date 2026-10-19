package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// PATCH /api/v1/nodes/{id} 只允许改名：成功 200、空名 400、未知节点 404、无 token 401。
func TestNodeRenameFlow(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/nodes/register", admin.Token, map[string]any{
		"name": "edge-before-rename", "os": "linux", "arch": "amd64", "version": "0.1.0",
	})
	requireStatus(t, status, http.StatusCreated)

	var created struct {
		NodeID string `json:"node_id"`
	}
	decodeResponse(t, body, &created)
	if created.NodeID == "" {
		t.Fatalf("register did not return node_id: %s", body)
	}

	// 改名成功
	status, body = mustRequest(t, app.server, http.MethodPatch, "/api/v1/nodes/"+created.NodeID, admin.Token, map[string]any{
		"name": "  edge-after-rename  ",
	})
	requireStatus(t, status, http.StatusOK)
	var renamed struct {
		Name string `json:"name"`
	}
	decodeResponse(t, body, &renamed)
	if renamed.Name != "edge-after-rename" {
		t.Fatalf("expected trimmed name edge-after-rename, got %q", renamed.Name)
	}

	// 再读一次，确认落库
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes/"+created.NodeID, admin.Token, nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &renamed)
	if renamed.Name != "edge-after-rename" {
		t.Fatalf("rename was not persisted, got %q", renamed.Name)
	}

	// 空名称 → 400
	status, _ = mustRequest(t, app.server, http.MethodPatch, "/api/v1/nodes/"+created.NodeID, admin.Token, map[string]any{
		"name": "   ",
	})
	requireStatus(t, status, http.StatusBadRequest)

	// 未知节点 → 404
	status, _ = mustRequest(t, app.server, http.MethodPatch, "/api/v1/nodes/00000000-0000-4000-8000-000000000000", admin.Token, map[string]any{
		"name": "nope",
	})
	requireStatus(t, status, http.StatusNotFound)

	// 未认证 → 401
	status, _ = mustRequest(t, app.server, http.MethodPatch, "/api/v1/nodes/"+created.NodeID, "", map[string]any{
		"name": "nope",
	})
	requireStatus(t, status, http.StatusUnauthorized)

	// 审计必须留下 node.update（沿用项目的审计中间件）
	var count int64
	if err := app.db.Raw("select count(*) from audit_logs where action = ?", "node.update").Scan(&count).Error; err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected an audit_logs row with action=node.update")
	}

	_ = json.Valid(body)
}
