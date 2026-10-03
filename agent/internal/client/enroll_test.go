package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrollReturnsMachineReadableAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":{"code":"enroll_token_exhausted","message":"enrollment token has no remaining uses"}}`))
	}))
	defer server.Close()
	_, err := New(server.URL, "").Enroll(context.Background(), EnrollRequest{Token: "secret", OS: "linux", Arch: "amd64", Version: "test"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusGone || apiErr.Code != "enroll_token_exhausted" {
		t.Fatalf("Enroll() error = %#v", err)
	}
}

func TestEnrollErrorRedactsEnrollmentToken(t *testing.T) {
	token := "neilico-enroll.payload-value.signature-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"bad token ` + token + `"}}`))
	}))
	defer server.Close()
	_, err := New(server.URL, "").Enroll(context.Background(), EnrollRequest{Token: token, OS: "linux", Arch: "amd64", Version: "test"})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("Enroll() error leaked enrollment token: %v", err)
	}
}
