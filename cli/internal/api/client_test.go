package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorRedactsSensitiveResponseFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad","message":"private_key\":\"secret\" agent_token\":\"token-secret\""}`))
	}))
	defer server.Close()
	err := New(server.URL, "token").Do(context.Background(), "GET", "/bad", nil, nil)
	if err == nil {
		t.Fatal("Do() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token-secret") {
		t.Fatalf("API error leaked sensitive value: %q", err.Error())
	}
}
