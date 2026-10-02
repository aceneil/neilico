package proxy

import (
	"crypto/tls"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestRedirectHTTPStatusAndExemptions(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := RedirectHTTP(next, ":8443")
	cases := []struct {
		method, path string
		status       int
		location     string
	}{
		{http.MethodGet, "/healthz", http.StatusOK, ""},
		{http.MethodGet, "/metrics", http.StatusOK, ""},
		{http.MethodGet, "/.well-known/acme-challenge/token", http.StatusNoContent, ""},
		{http.MethodGet, "/api/v1/nodes?x=1", http.StatusMovedPermanently, "https://api.example.test:8443/api/v1/nodes?x=1"},
		{http.MethodPost, "/api/v1/nodes", http.StatusPermanentRedirect, "https://api.example.test:8443/api/v1/nodes"},
	}
	for _, item := range cases {
		request := httptest.NewRequest(item.method, item.path, nil)
		request.Host = "api.example.test:8080"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != item.status || recorder.Header().Get("Location") != item.location {
			t.Fatalf("%s %s = %d %q, want %d %q", item.method, item.path, recorder.Code, recorder.Header().Get("Location"), item.status, item.location)
		}
	}
}

func TestSecurityHeadersHSTSOnlyOverTLSAndZeroDisabled(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	plainRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	SecurityHeaders(next, 31536000).ServeHTTP(recorder, plainRequest)
	if recorder.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS emitted on plaintext request")
	}
	tlsRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	tlsRequest.TLS = &tls.ConnectionState{}
	recorder = httptest.NewRecorder()
	SecurityHeaders(next, 31536000).ServeHTTP(recorder, tlsRequest)
	if recorder.Header().Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("HSTS = %q", recorder.Header().Get("Strict-Transport-Security"))
	}
	recorder = httptest.NewRecorder()
	SecurityHeaders(next, 0).ServeHTTP(recorder, tlsRequest)
	if recorder.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS emitted when max age is zero")
	}
}

func TestHTTPSupstreamTransportModes(t *testing.T) {
	t.Parallel()
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secure backend")
	}))
	defer backend.Close()
	route := Route{UpstreamScheme: "https"}
	target := mustParseURL(t, backend.URL)
	if _, err := upstreamTransport(route, target); err != nil {
		t.Fatal(err)
	}
	route.UpstreamCAFile = filepath.Join(t.TempDir(), "ca.pem")
	data := pemCertificate(t, backend)
	if err := os.WriteFile(route.UpstreamCAFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, err := upstreamTransport(route, target)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: trusted}
	response, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("trusted https upstream failed: %v", err)
	}
	response.Body.Close()
	route.UpstreamInsecureSkipVerify = true
	route.UpstreamCAFile = ""
	insecure, err := upstreamTransport(route, target)
	if err != nil {
		t.Fatal(err)
	}
	response, err = (&http.Client{Transport: insecure}).Get(backend.URL)
	if err != nil {
		t.Fatalf("insecure https upstream failed: %v", err)
	}
	response.Body.Close()
}

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func pemCertificate(t *testing.T, server *httptest.Server) []byte {
	t.Helper()
	cert := server.Certificate()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}
