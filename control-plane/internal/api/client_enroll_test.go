package api_test

import (
	"net/http"
	"testing"
)

type clientEnrollResponse struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Server   struct {
		IDServer    string `json:"id_server"`
		RelayServer string `json:"relay_server"`
		APIServer   string `json:"api_server"`
		Key         string `json:"key"`
	} `json:"server"`
	APIToken string `json:"api_token"`
}

func TestClientEnrollSuccess(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	token := createClientEnrollToken(t, app, admin.Token)

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/client/enroll", "", map[string]any{"token": token})
	requireStatus(t, status, http.StatusOK)
	var response clientEnrollResponse
	decodeResponse(t, body, &response)
	if response.NodeID == "" {
		t.Fatal("node_id is empty")
	}
	if response.NodeName == "" {
		t.Fatal("node_name is empty")
	}
	if response.APIToken == "" {
		t.Fatal("api_token is empty")
	}
	status, _ = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", response.APIToken, nil)
	requireStatus(t, status, http.StatusOK)
}

func TestClientEnrollInvalidToken(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/client/enroll", "", map[string]any{"token": "neilico-enroll.not-a-real-token"})
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_enroll_token")
}

func TestClientEnrollTokenIsSingleUse(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	token := createClientEnrollToken(t, app, admin.Token)

	status, _ := mustRequest(t, app.server, http.MethodPost, "/api/v1/client/enroll", "", map[string]any{"token": token})
	requireStatus(t, status, http.StatusOK)

	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/client/enroll", "", map[string]any{"token": token})
	requireStatus(t, status, http.StatusGone)
	requireErrorCode(t, body, "enroll_token_exhausted")
}

func createClientEnrollToken(t *testing.T, app testApp, adminToken string) string {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/enroll-tokens", adminToken, map[string]any{
		"name_hint": "client-test",
		"max_uses":  1,
	})
	requireStatus(t, status, http.StatusCreated)
	var response struct {
		Token string `json:"token"`
	}
	decodeResponse(t, body, &response)
	if response.Token == "" {
		t.Fatal("enroll token is empty")
	}
	return response.Token
}
