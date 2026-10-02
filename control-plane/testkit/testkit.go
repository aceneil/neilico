// Package testkit exposes an in-memory control-plane handler for cross-module integration tests.
package testkit

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/api"
	"neilico/control-plane/internal/auth"
	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/metrics"
	"neilico/control-plane/internal/service"
)

const (
	AdminEmail    = "m3-admin@example.test"
	AdminPassword = "m3-bootstrap-password"
)

type App struct {
	Server  *httptest.Server
	DB      *gorm.DB
	Handler http.Handler
}

func New(t testing.TB) *App {
	t.Helper()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared"}, "error")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	if err := service.Bootstrap(context.Background(), handle, config.Bootstrap{
		AdminEmail:    AdminEmail,
		AdminPassword: AdminPassword,
		DefaultTenant: "m3",
	}); err != nil {
		t.Fatalf("bootstrap test control plane: %v", err)
	}
	manager, err := auth.NewManager("0123456789abcdef0123456789abcdef", time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatalf("create auth manager: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	nodeService := service.NewNodeService(handle, time.Minute)
	promMetrics := metrics.New(handle)
	handler := api.New(handle, manager, nodeService, promMetrics, logger, "m3-test")
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return &App{Server: server, DB: handle, Handler: handler}
}
