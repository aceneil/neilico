package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/service"
	acmeclient "neilico/control-plane/internal/service/cert/acme"
	"neilico/control-plane/internal/service/proxy"
)

func TestCertificatePermissionsAndTenantIsolation(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")

	var roleUsers []struct {
		email string
		role  string
		token string
	}
	for _, role := range []string{auth.RoleOps, auth.RoleReadonly} {
		email := role + "@bootstrap.test"
		status, body := mustRequest(t, app.server, "POST", "/api/v1/users", admin.Token, map[string]any{
			"email": email, "password": role + "-password", "role": role,
		})
		requireStatus(t, status, 201)
		_ = body
		login := mustLogin(t, app, email, role+"-password")
		roleUsers = append(roleUsers, struct {
			email string
			role  string
			token string
		}{email: email, role: role, token: login.Token})
	}
	for _, user := range roleUsers {
		status, body := mustRequest(t, app.server, "POST", "/api/v1/certificates", user.token, map[string]any{
			"issuer": "acme", "domain": "forbidden.example.test",
		})
		requireStatus(t, status, 403)
		requireErrorCode(t, body, "forbidden")
		status, body = mustRequest(t, app.server, "GET", "/api/v1/certificates", user.token, nil)
		requireStatus(t, status, 200)
	}

	certPEM, keyPEM := mustCertificate(t, "owned.example.test")
	status, body := mustRequest(t, app.server, "POST", "/api/v1/certificates", admin.Token, map[string]any{
		"cert_pem": certPEM, "key_pem": keyPEM,
	})
	requireStatus(t, status, 201)
	var owned struct {
		ID string `json:"id"`
	}
	decodeResponse(t, body, &owned)

	status, body = mustRequest(t, app.server, "POST", "/api/v1/tenants", admin.Token, map[string]any{"name": "certificate-other", "plan": "free"})
	requireStatus(t, status, 201)
	var otherTenant struct {
		ID uuid.UUID `json:"id"`
	}
	decodeResponse(t, body, &otherTenant)
	status, body = mustRequest(t, app.server, "POST", "/api/v1/users", admin.Token, map[string]any{
		"tenant_id": otherTenant.ID, "email": "other-cert-admin@example.test", "password": "other-cert-password", "role": auth.RoleTenantAdmin,
	})
	requireStatus(t, status, 201)
	otherAdmin := mustLogin(t, app, "other-cert-admin@example.test", "other-cert-password")
	for _, test := range []struct{ method, path string }{
		{"GET", "/api/v1/certificates/" + owned.ID},
		{"POST", "/api/v1/certificates/" + owned.ID + "/renew"},
		{"POST", "/api/v1/certificates/" + owned.ID + "/revoke"},
		{"DELETE", "/api/v1/certificates/" + owned.ID},
	} {
		status, body = mustRequest(t, app.server, test.method, test.path, otherAdmin.Token, nil)
		requireStatus(t, status, 404)
		requireErrorCode(t, body, "not_found")
	}
}

func TestCertificateAPIStableACMEGuards(t *testing.T) {
	for _, test := range []struct {
		name string
		cfg  acmeclient.Config
		code string
	}{
		{name: "disabled", cfg: acmeclient.Config{Challenge: acmeclient.ChallengeHTTP01}, code: "acme_disabled"},
		{name: "tos", cfg: acmeclient.Config{Enabled: true, Challenge: acmeclient.ChallengeHTTP01}, code: "acme_tos_not_accepted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newTestAppWithACME(t, acmeclient.New(test.cfg, nil), service.ACMEOptions{ChallengeSolver: acmeclient.NewChallengeStore(time.Hour)})
			defer app.server.Close()
			admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
			status, body := mustRequest(t, app.server, "POST", "/api/v1/certificates", admin.Token, map[string]any{
				"issuer": "acme", "domain": "guard.example.test",
			})
			requireStatus(t, status, 409)
			requireErrorCode(t, body, test.code)
		})
	}
}

func newTestAppWithACME(t *testing.T, client acmeclient.Client, options service.ACMEOptions) testApp {
	t.Helper()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared"}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(context.Background(), handle, config.Bootstrap{
		AdminEmail: "admin@example.test", AdminPassword: "bootstrap-password", DefaultTenant: "bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(testJWTSecret, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	nodeService := service.NewNodeService(handle, time.Minute)
	promMetrics := metrics.New(handle)
	builtinProxy := proxy.NewBuiltin(handle, manager, logger, promMetrics)
	handler := api.NewWithProxy(handle, manager, nodeService, promMetrics, logger, "test", builtinProxy, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		ACME: client, ACMEOptions: options,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return testApp{t: t, server: server, db: handle, handler: handler, proxy: builtinProxy}
}
