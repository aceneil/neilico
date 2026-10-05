package api_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
	"neilico/control-plane/internal/service/proxy"
)

// 满足强度要求（>=16 字符，含大小写/数字/符号）的测试口令。
const (
	strongPassword    = "Correct-Horse-Battery-9!"
	rotatedPassword   = "Rotated-Horse-Battery-7!"
	tooWeakPassword   = "short-password"
	registrationEmail = "first-admin@example.test"
)

// newRegistrableApp 构造一个「没有预置管理员」的应用，用来验证首次注册流程；
// envFile 非空时启用密码轮换后的 env 回写。
func newRegistrableApp(t *testing.T, envFile string) testApp {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn}, "error")
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatalf("db.AutoMigrate() error = %v", err)
	}
	manager, err := auth.NewManager(testJWTSecret, time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	nodeService := service.NewNodeService(handle, time.Minute)
	sweeper := service.NewNodeSweeper(handle, time.Minute, logger)
	promMetrics := metrics.New(handle)
	builtinProxy := proxy.NewBuiltin(handle, manager, logger, promMetrics)
	handler := api.NewWithProxy(handle, manager, nodeService, promMetrics, logger, "test", builtinProxy, api.ProxyOptions{
		Enabled: true,
		Kind:    "builtin",
		Listen:  "127.0.0.1:0",
		Bootstrap: api.BootstrapOptions{
			EnvFile:       envFile,
			DefaultTenant: "default",
		},
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return testApp{t: t, server: server, db: handle, sweeper: sweeper, handler: handler, proxy: builtinProxy}
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "neilico.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetupStatusAndFirstRegistrationFlow(t *testing.T) {
	app := newRegistrableApp(t, "")
	defer app.server.Close()

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK)
	var setup struct {
		Initialized      bool `json:"initialized"`
		RegistrationOpen bool `json:"registration_open"`
		PasswordPolicy   struct {
			MinLength int `json:"min_length"`
		} `json:"password_policy"`
	}
	decodeResponse(t, body, &setup)
	if setup.Initialized || !setup.RegistrationOpen {
		t.Fatalf("fresh system should be uninitialized and open: %#v", setup)
	}
	if setup.PasswordPolicy.MinLength < 16 {
		t.Fatalf("password policy weaker than required: %#v", setup.PasswordPolicy)
	}

	// 弱密码被拒。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/setup/register", "", map[string]any{
		"email": registrationEmail, "password": tooWeakPassword,
	})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")

	// 合规密码注册成功并直接返回会话。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/setup/register", "", map[string]any{
		"email": registrationEmail, "password": strongPassword,
	})
	requireStatus(t, status, http.StatusCreated)
	var created loginResponse
	decodeResponse(t, body, &created)
	if created.Token == "" || created.RefreshToken == "" || created.User.Role != auth.RolePlatformAdmin {
		t.Fatalf("registration did not return an admin session: %#v", created)
	}

	// 已有账号：状态翻转 + 再次注册被拒（409）。
	status, body = mustRequest(t, app.server, http.MethodGet, "/api/v1/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK)
	decodeResponse(t, body, &setup)
	if !setup.Initialized || setup.RegistrationOpen {
		t.Fatalf("system should be initialized after registration: %#v", setup)
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/setup/register", "", map[string]any{
		"email": "second-admin@example.test", "password": strongPassword,
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "already_initialized")

	// 注册后可用新凭据登录。
	mustLogin(t, app, registrationEmail, strongPassword)
}

func TestBootstrappedSystemReportsInitializedAndRejectsRegistration(t *testing.T) {
	app := newTestApp(t) // 预置了 env 引导管理员
	defer app.server.Close()

	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/setup/status", "", nil)
	requireStatus(t, status, http.StatusOK)
	var setup struct {
		Initialized      bool `json:"initialized"`
		RegistrationOpen bool `json:"registration_open"`
	}
	decodeResponse(t, body, &setup)
	if !setup.Initialized || setup.RegistrationOpen {
		t.Fatalf("bootstrapped system should be initialized: %#v", setup)
	}
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/setup/register", "", map[string]any{
		"email": "sneaky@example.test", "password": strongPassword,
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "already_initialized")
}

func TestAccountEmailChangeValidatesPasswordAndConflict(t *testing.T) {
	envPath := writeEnvFile(t, strings.Join([]string{
		"POSTGRES_PASSWORD=db-secret-must-not-change",
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL=first-admin@example.test",
		"NEILICO_BOOTSTRAP_ADMIN_PASSWORD=keep-me-out-of-sight",
		"NEILICO_LOG_LEVEL=info",
		"",
	}, "\n"))
	app := newRegistrableApp(t, envPath)
	defer app.server.Close()

	admin := registerAdmin(t, app, registrationEmail, strongPassword)

	// 建一个占用邮箱的第二个用户。
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/users", admin.Token, map[string]any{
		"email": "occupied@example.test", "password": "second-user-password", "role": auth.RoleOps,
	})
	requireStatus(t, status, http.StatusCreated)

	// 当前密码错误 → 403 invalid_password。
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/account/email", admin.Token, map[string]any{
		"current_password": "wrong-password", "email": "renamed@example.test",
	})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "invalid_password")

	// 目标邮箱被占用 → 409。
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/account/email", admin.Token, map[string]any{
		"current_password": strongPassword, "email": "occupied@example.test",
	})
	requireStatus(t, status, http.StatusConflict)
	requireErrorCode(t, body, "conflict")

	// 正常改名 → 200 + env 回写新邮箱，其它行不动。
	status, body = mustRequest(t, app.server, http.MethodPut, "/api/v1/account/email", admin.Token, map[string]any{
		"current_password": strongPassword, "email": "renamed@example.test",
	})
	requireStatus(t, status, http.StatusOK)
	var changed struct {
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
		EnvFileUpdated bool `json:"env_file_updated"`
	}
	decodeResponse(t, body, &changed)
	if changed.Account.Email != "renamed@example.test" || !changed.EnvFileUpdated {
		t.Fatalf("unexpected email-change response: %#v", changed)
	}
	env := readFile(t, envPath)
	if !strings.Contains(env, "NEILICO_BOOTSTRAP_ADMIN_EMAIL=renamed@example.test\n") {
		t.Fatalf("env email not rewritten:\n%s", env)
	}
	if !strings.Contains(env, "POSTGRES_PASSWORD=db-secret-must-not-change\n") ||
		!strings.Contains(env, "NEILICO_LOG_LEVEL=info\n") {
		t.Fatalf("env unrelated lines changed:\n%s", env)
	}

	// 新邮箱可登录，旧邮箱不可。
	mustLogin(t, app, "renamed@example.test", strongPassword)
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": registrationEmail, "password": strongPassword,
	})
	requireStatus(t, status, http.StatusUnauthorized)
}

func TestPasswordRotationInvalidatesOldTokensAndRewritesEnv(t *testing.T) {
	envPath := writeEnvFile(t, strings.Join([]string{
		"POSTGRES_PASSWORD=db-secret-must-not-change",
		"NEILICO_BOOTSTRAP_ADMIN_EMAIL=first-admin@example.test",
		"NEILICO_BOOTSTRAP_ADMIN_PASSWORD=old-password-in-env",
		"NEILICO_LOG_LEVEL=info",
		"",
	}, "\n"))
	app := newRegistrableApp(t, envPath)
	defer app.server.Close()

	admin := registerAdmin(t, app, registrationEmail, strongPassword)

	// 当前密码错误。
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/account/password/rotate", admin.Token, map[string]any{
		"current_password": "not-the-password", "new_password": rotatedPassword,
	})
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "invalid_password")

	// 新密码强度不足。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/account/password/rotate", admin.Token, map[string]any{
		"current_password": strongPassword, "new_password": tooWeakPassword,
	})
	requireStatus(t, status, http.StatusBadRequest)
	requireErrorCode(t, body, "invalid_request")

	// 正常轮换 → 200 + env 回写 + 新会话。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/account/password/rotate", admin.Token, map[string]any{
		"current_password": strongPassword, "new_password": rotatedPassword,
	})
	requireStatus(t, status, http.StatusOK)
	var rotated struct {
		Token          string `json:"token"`
		RefreshToken   string `json:"refresh_token"`
		EnvFileUpdated bool   `json:"env_file_updated"`
	}
	decodeResponse(t, body, &rotated)
	if !rotated.EnvFileUpdated || rotated.Token == "" || rotated.RefreshToken == "" {
		t.Fatalf("unexpected rotate response flags: %#v", rotated)
	}
	if strings.Contains(string(body), rotatedPassword) {
		// 响应本身不应回显新密码（只回写文件）。
		t.Fatal("rotate response leaked the new password")
	}

	// env 回写：目标键被替换，其它行保留，旧值消失。
	env := readFile(t, envPath)
	if !strings.Contains(env, "NEILICO_BOOTSTRAP_ADMIN_PASSWORD="+rotatedPassword+"\n") {
		t.Fatalf("env password not rewritten:\n%s", env)
	}
	if strings.Contains(env, "old-password-in-env") {
		t.Fatalf("old env password lingering:\n%s", env)
	}
	if !strings.Contains(env, "POSTGRES_PASSWORD=db-secret-must-not-change\n") ||
		!strings.Contains(env, "NEILICO_BOOTSTRAP_ADMIN_EMAIL=first-admin@example.test\n") {
		t.Fatalf("env unrelated lines changed:\n%s", env)
	}

	// 旧密码失效，新密码可用。
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": registrationEmail, "password": strongPassword,
	})
	requireStatus(t, status, http.StatusUnauthorized)
	mustLogin(t, app, registrationEmail, rotatedPassword)

	// 旧 refresh token 失效；rotate 返回的新 refresh token 仍可用。
	status, body = mustRequest(t, app.server, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": admin.RefreshToken,
	})
	requireStatus(t, status, http.StatusUnauthorized)
	requireErrorCode(t, body, "invalid_token")
	status, _ = mustRequest(t, app.server, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": rotated.RefreshToken,
	})
	requireStatus(t, status, http.StatusOK)

	// 审计留痕（不得含明文口令）。
	var audits int64
	if err := app.db.Model(&models.AuditLog{}).Where("action = ?", "account.password.rotate").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("password rotate audit records = %d, want 1", audits)
	}
}

func TestAccountRequiresUserSessionNotAPIToken(t *testing.T) {
	app := newTestApp(t)
	defer app.server.Close()
	admin := mustLogin(t, app, "admin@example.test", "bootstrap-password")
	created := createAPIToken(t, app, admin.Token, map[string]any{
		"name": "self-service-should-be-blocked", "scopes": []string{auth.ScopeAdmin},
	})
	status, body := mustRequest(t, app.server, http.MethodGet, "/api/v1/account", created.Token, nil)
	requireStatus(t, status, http.StatusForbidden)
	requireErrorCode(t, body, "forbidden")
}

func registerAdmin(t *testing.T, app testApp, email, password string) loginResponse {
	t.Helper()
	status, body := mustRequest(t, app.server, http.MethodPost, "/api/v1/setup/register", "", map[string]any{
		"email": email, "password": password,
	})
	requireStatus(t, status, http.StatusCreated)
	var response loginResponse
	decodeResponse(t, body, &response)
	return response
}
