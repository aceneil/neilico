package client

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIErrorRedactsTokenPrivateAndAgentTokenFields(t *testing.T) {
	token := "super-secret-agent-token"
	privateKey := "PRIVATE-KEY-MUST-NOT-LEAK"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","message":"` + token + ` ` + privateKey + ` \"private_key\":\"` + privateKey + `\" \"agent_token\":\"` + token + `\" PrivateKey = ` + privateKey + `"}`))
	}))
	defer server.Close()
	api := New(server.URL, token)
	_, err := api.Register(context.Background(), RegisterRequest{Name: "node"})
	if err == nil {
		t.Fatal("Register() unexpectedly succeeded")
	}
	message := err.Error()
	if strings.Contains(message, token) || strings.Contains(message, privateKey) {
		t.Fatalf("API error leaked secret: %q", message)
	}
	if !strings.Contains(message, "***") {
		t.Fatalf("API error was not visibly redacted: %q", message)
	}
}

func TestConfig304DoesNotDecodeDelivery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotModified)
		_, _ = w.Write([]byte(`{"not_modified":true,"version":7}`))
	}))
	defer server.Close()
	result, err := New(server.URL, "token").Config(context.Background(), "node", 7)
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if !result.NotModified || result.Version != 7 {
		t.Fatalf("Config() = %#v", result)
	}
}

func TestLogsDoNotLeakTokenOrPrivateKey(t *testing.T) {
	token := "log-token-full-value"
	privateKey := "log-private-key-full-value"
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	err := SafeError(errors.New("agent_token="+token+" private_key="+privateKey), token, privateKey)
	logger.Warn("heartbeat failed", "error", err)
	text := output.String()
	if strings.Contains(text, token) || strings.Contains(text, privateKey) {
		t.Fatalf("log leaked secret: %q", text)
	}
	if !strings.Contains(text, "***") {
		t.Fatalf("log did not show redaction: %q", text)
	}
}
