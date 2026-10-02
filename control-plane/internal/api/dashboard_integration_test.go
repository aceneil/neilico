package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardDisabledPreservesUnknownRouteBehavior(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()

	status, body := mustRequest(t, app.server, http.MethodGet, "/nodes", "", nil)
	requireStatus(t, status, http.StatusNotFound)
	if strings.Contains(string(body), "<!doctype html>") {
		t.Fatalf("disabled dashboard served a frontend route: %s", body)
	}
}

func TestDashboardStaticSPATraversalAndExistingRoutePriority(t *testing.T) {
	dir := t.TempDir()
	index := "<!doctype html><title>NEILICO Dashboard</title>"
	writeDashboardFile(t, filepath.Join(dir, "index.html"), index)
	writeDashboardFile(t, filepath.Join(dir, "assets", "app-A1B2C3.js"), "window.app = true")
	writeDashboardFile(t, filepath.Join(dir, "assets", "theme-D4E5F6.css"), ":root{}")
	writeDashboardFile(t, filepath.Join(filepath.Dir(dir), "outside.js"), "outside")
	app := newTestAppWithDashboard(t, dir, true)
	defer app.server.Close()

	status, body := mustRequest(t, app.server, http.MethodGet, "/assets/app-A1B2C3.js", "", nil)
	requireStatus(t, status, http.StatusOK)
	if string(body) != "window.app = true" {
		t.Fatalf("asset body = %q", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/nodes", "", nil)
	requireStatus(t, status, http.StatusOK)
	if string(body) != index {
		t.Fatalf("SPA body = %q", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/missing.js", "", nil)
	requireStatus(t, status, http.StatusNotFound)
	if strings.Contains(string(body), index) {
		t.Fatal("missing static asset fell back to index.html")
	}

	for _, requestPath := range []string{"/../outside.js", "/..%2foutside.js"} {
		request, err := http.NewRequest(http.MethodGet, app.server.URL+requestPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := app.server.Client()
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		payload := make([]byte, 64)
		n, _ := response.Body.Read(payload)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound || strings.Contains(string(payload[:n]), "outside") {
			t.Fatalf("traversal %q response = %d %q", requestPath, response.StatusCode, payload[:n])
		}
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/nodes", "", nil)
	requireStatus(t, status, http.StatusUnauthorized)
	if !strings.Contains(string(body), `"code":"unauthorized"`) {
		t.Fatalf("API route was hijacked: %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/api/legacy", "", nil)
	requireStatus(t, status, http.StatusNotFound)
	if strings.Contains(string(body), index) {
		t.Fatalf("unknown /api/* route was hijacked: %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/healthz", "", nil)
	requireStatus(t, status, http.StatusOK)
	if !strings.Contains(string(body), `"db":"up"`) {
		t.Fatalf("health route was hijacked: %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/metrics", "", nil)
	requireStatus(t, status, http.StatusOK)
	if !strings.Contains(string(body), "neilico_http_requests_total") {
		t.Fatalf("metrics route was hijacked: %s", body)
	}

	status, body = mustRequest(t, app.server, http.MethodGet, "/.well-known/acme-challenge/x", "", nil)
	requireStatus(t, status, http.StatusOK)
	if string(body) != "challenge-handler" {
		t.Fatalf("challenge route was hijacked: %q", body)
	}
}

func TestDashboardAssetMetricsAreNormalized(t *testing.T) {
	dir := t.TempDir()
	writeDashboardFile(t, filepath.Join(dir, "index.html"), "index")
	writeDashboardFile(t, filepath.Join(dir, "assets", "one-A1.js"), "one")
	writeDashboardFile(t, filepath.Join(dir, "assets", "two-B2.js"), "two")
	app := newTestAppWithDashboard(t, dir, true)
	defer app.server.Close()

	for _, assetPath := range []string{"/assets/one-A1.js", "/assets/two-B2.js"} {
		status, _ := mustRequest(t, app.server, http.MethodGet, assetPath, "", nil)
		requireStatus(t, status, http.StatusOK)
	}
	status, body := mustRequest(t, app.server, http.MethodGet, "/metrics", "", nil)
	requireStatus(t, status, http.StatusOK)
	text := string(body)
	if !strings.Contains(text, `neilico_http_requests_total{method="GET",path="/assets/*",status="200"} 2`) {
		t.Fatalf("normalized asset metric missing:\n%s", text)
	}
	if strings.Contains(text, "one-A1.js") || strings.Contains(text, "two-B2.js") {
		t.Fatalf("asset filenames leaked into metric labels:\n%s", text)
	}
}

func writeDashboardFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
