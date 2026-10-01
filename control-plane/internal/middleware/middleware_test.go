package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/models"
)

func TestAuthRequiredAPITokenStatesAndUsageThrottle(t *testing.T) {
	handle := newMiddlewareDB(t)
	manager, err := auth.NewManager("0123456789abcdef0123456789abcdef", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewAPITokenUsageTracker(handle, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.AuthMethod != auth.AuthMethodAPIToken || principal.TokenPrefix == "" {
			t.Fatal("API token principal was not injected")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := AuthRequired(manager, handle, tracker, next)
	tenantID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "tokens", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeToken := func(name string, expires *time.Time, revoked *time.Time) (string, models.APIToken) {
		plain, hash, prefix, err := auth.GenerateAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		item := models.APIToken{
			ID: uuid.New(), TenantID: tenantID, Name: name, TokenHash: hash,
			TokenPrefix: prefix, Scopes: []string{auth.ScopeNodesRead},
			ExpiresAt: expires, RevokedAt: revoked, CreatedAt: now,
		}
		if err := handle.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		return plain, item
	}
	valid, validItem := makeToken("valid", nil, nil)
	expiredAt := now.Add(-time.Minute)
	expired, _ := makeToken("expired", &expiredAt, nil)
	revokedAt := now.Add(-time.Minute)
	revoked, _ := makeToken("revoked", nil, &revokedAt)

	for _, test := range []struct {
		name, token, code string
		status            int
	}{
		{name: "valid", token: valid, code: "", status: http.StatusNoContent},
		{name: "invalid", token: "umpp_invalid", code: "invalid_token", status: http.StatusUnauthorized},
		{name: "expired", token: expired, code: "token_expired", status: http.StatusUnauthorized},
		{name: "revoked", token: revoked, code: "token_revoked", status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
			request.RemoteAddr = "192.0.2.10:1234"
			request.Header.Set("Authorization", "Bearer "+test.token)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if test.code != "" {
				var payload struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.Error.Code != test.code {
					t.Fatalf("error code = %q, want %q", payload.Error.Code, test.code)
				}
			}
			if bytes.Contains(recorder.Body.Bytes(), []byte(test.token)) {
				t.Fatal("response leaked credential")
			}
		})
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var stored models.APIToken
		if err := handle.First(&stored, "id = ?", validItem.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.LastUsedAt != nil && stored.LastUsedIP == "192.0.2.10" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("API token usage was not updated asynchronously")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tracker.ShouldUpdate(validItem.ID) {
		t.Fatal("second usage write was not throttled")
	}
}

func TestRequireScopeAndRedaction(t *testing.T) {
	granted := []string{auth.ScopeNodesRead}
	yes := RequireScope(auth.ScopeNodesRead)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	ctx := context.WithValue(request.Context(), principalKey, Principal{AuthMethod: auth.AuthMethodAPIToken, Scopes: granted})
	recorder := httptest.NewRecorder()
	yes.ServeHTTP(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("read scope status = %d", recorder.Code)
	}

	no := RequireScope(auth.ScopeNodesWrite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("insufficient scope reached handler")
	}))
	recorder = httptest.NewRecorder()
	no.ServeHTTP(recorder, request.WithContext(ctx))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), `"code":"insufficient_scope"`) {
		t.Fatalf("insufficient scope response = %d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "umpp_") {
		t.Fatal("scope error leaked token")
	}

	plain := "umpp_" + strings.Repeat("A", 43)
	detail := marshalDetail(map[string]any{"token": plain, "message": "used " + plain})
	if bytes.Contains(detail, []byte(plain)) || !bytes.Contains(detail, []byte(auth.RedactAPIToken(plain))) {
		t.Fatalf("audit detail was not redacted: %s", detail)
	}
}

func TestRateLimitBurstRecoveryIsolationAndExemptions(t *testing.T) {
	exemptLimiter := NewLimiter(0.001, 1)
	exemptHandler := RateLimit(exemptLimiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	exemptKey := Principal{AuthMethod: auth.AuthMethodAPIToken, APITokenID: uuid.MustParse("33333333-3333-3333-3333-333333333333")}
	for _, path := range []string{"/healthz", "/metrics", "/.well-known/acme-challenge/token"} {
		for index := 0; index < 4; index++ {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request = request.WithContext(context.WithValue(request.Context(), principalKey, exemptKey))
			recorder := httptest.NewRecorder()
			exemptHandler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("exempt path %s status = %d", path, recorder.Code)
			}
		}
	}
	protected := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	protected = protected.WithContext(context.WithValue(protected.Context(), principalKey, exemptKey))
	recorder := httptest.NewRecorder()
	exemptHandler.ServeHTTP(recorder, protected)
	if recorder.Code != http.StatusNoContent {
		t.Fatal("exempt requests consumed protected-path bucket")
	}

	limiter := NewLimiter(1, 2)
	now := time.Unix(100, 0)
	limiter.now = func() time.Time { return now }
	handler := RateLimit(limiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	newRequest := func(path string) *http.Request {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		return request.WithContext(context.WithValue(request.Context(), principalKey, Principal{
			AuthMethod: auth.AuthMethodAPIToken, APITokenID: uuid.New(),
		}))
	}
	first := newRequest("/api/v1/nodes")
	first = first.WithContext(context.WithValue(first.Context(), principalKey, Principal{
		AuthMethod: auth.AuthMethodAPIToken, APITokenID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}))
	for index := 0; index < 2; index++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, first)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("burst request %d status = %d", index, recorder.Code)
		}
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, first)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit response = %d retry=%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.Error.Code != "rate_limited" {
		t.Fatalf("rate limit error = %s", recorder.Body.String())
	}

	second := newRequest("/api/v1/nodes")
	second = second.WithContext(context.WithValue(second.Context(), principalKey, Principal{
		AuthMethod: auth.AuthMethodAPIToken, APITokenID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, second)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("isolated key status = %d", recorder.Code)
	}

	for _, path := range []string{"/healthz", "/metrics", "/.well-known/acme-challenge/token"} {
		for index := 0; index < 5; index++ {
			recorder = httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("exempt path %s status = %d", path, recorder.Code)
			}
		}
	}
	now = now.Add(2 * time.Second)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, first)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("recovered key status = %d", recorder.Code)
	}
}

func newMiddlewareDB(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, err := handle.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return handle
}
