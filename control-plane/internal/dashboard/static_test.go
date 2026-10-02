package dashboard_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"neilico/control-plane/internal/dashboard"
)

func TestStaticFilesSPAFallbackAndCacheHeaders(t *testing.T) {
	dir := t.TempDir()
	index := "<!doctype html><title>NEILICO</title>"
	writeTestFile(t, filepath.Join(dir, "index.html"), index)
	writeTestFile(t, filepath.Join(dir, "assets", "app-B1C2D3.js"), "console.log('ok')")
	handler := dashboard.New(dashboard.Options{Dir: dir, SPA: true})

	recorder := doRequest(t, handler, http.MethodGet, "/assets/app-B1C2D3.js")
	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		recorder.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset response = %d %#v", recorder.Code, recorder.Header())
	}

	recorder = doRequest(t, handler, http.MethodGet, "/nodes")
	if recorder.Code != http.StatusOK || recorder.Body.String() != index ||
		recorder.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA fallback response = %d %#v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	recorder = doRequest(t, handler, http.MethodGet, "/missing.js")
	if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), index) {
		t.Fatalf("missing asset response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestStaticTraversalIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "index.html"), "index")
	writeTestFile(t, filepath.Join(filepath.Dir(dir), "outside.txt"), "outside")
	handler := dashboard.New(dashboard.Options{Dir: dir, SPA: true})

	for _, requestPath := range []string{"/../outside.txt", "/..%2foutside.txt", "/assets/../../outside.txt"} {
		recorder := doRequest(t, handler, http.MethodGet, requestPath)
		if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "outside") {
			t.Fatalf("traversal %q response = %d %q", requestPath, recorder.Code, recorder.Body.String())
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func doRequest(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	_, _ = io.Copy(io.Discard, recorder.Result().Body)
	return recorder
}
