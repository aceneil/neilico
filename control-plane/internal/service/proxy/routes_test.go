package proxy

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	"neilico/control-plane/internal/models"
	"neilico/control-plane/internal/service"
)

func TestResolveNodeTarget(t *testing.T) {
	t.Parallel()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())}, "error")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := handle.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := handle.AutoMigrate(&models.Tenant{}, &models.Node{}, &models.Domain{}, &models.ProxyRule{}); err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.New()
	nodeID := uuid.New()
	virtualIP := "100.64.0.10"
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "target-test"}).Error; err != nil {
		t.Fatal(err)
	}
	node := models.Node{
		ID:             nodeID,
		TenantID:       tenantID,
		Name:           "node",
		PublicKey:      "key",
		VirtualIP:      &virtualIP,
		OS:             "linux",
		Arch:           "amd64",
		Version:        "test",
		Status:         service.NodeStatusOffline,
		AgentTokenHash: uuid.NewString(),
	}
	if err := handle.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	rule := models.ProxyRule{TargetType: "node", Target: nodeID.String() + ":8080", TenantID: tenantID}
	target, err := resolveTarget(context.Background(), handle, rule)
	if err != nil {
		t.Fatal(err)
	}
	if target != "100.64.0.10:8080" {
		t.Fatalf("resolved target = %q", target)
	}
	node.VirtualIP = nil
	if err := handle.Save(&node).Error; err != nil {
		t.Fatal(err)
	}
	_, err = resolveTarget(context.Background(), handle, rule)
	if !errors.Is(err, ErrUnprocessable) {
		t.Fatalf("missing virtual IP error = %v, want ErrUnprocessable", err)
	}
}
