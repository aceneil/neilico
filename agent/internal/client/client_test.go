package client

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestTLSOptionsTrustCustomCAAndClientCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	// The test server's self-signed certificate is sufficient as the trust anchor.
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := NewWithTLS(server.URL, "token", TLSOptions{CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Heartbeat(context.Background(), "node", "test"); err != nil {
		t.Fatalf("trusted TLS request failed: %v", err)
	}
	if _, err := NewWithTLS(server.URL, "token", TLSOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithTLS(server.URL, "token", TLSOptions{CAFile: filepath.Join(dir, "missing.pem")}); err == nil {
		t.Fatal("missing CA file unexpectedly accepted")
	}
}

func TestTLSWithoutCustomCAFailsCertificateVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	api := New(server.URL, "token")
	_, err := api.Heartbeat(context.Background(), "node", "test")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("expected certificate verification error, got %v", err)
	}
}
