package config

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	appconfig "umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service/cert"
)

func TestNodeConfigSnapshotGoldenAndDeterministic(t *testing.T) {
	handle, err := db.Open(appconfig.Database{
		Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared",
	}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	crypto, err := cert.NewCryptoFromKey([]byte("config-golden-key"))
	if err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := crypto.Encrypt("NETWORK_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	networkID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	nodeAID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	nodeBID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	joined := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tenant := models.Tenant{ID: tenantID, Name: "golden"}
	network := models.VirtualNetwork{ID: networkID, TenantID: tenantID, Name: "home", CIDR: "100.64.0.0/24", Secret: encryptedSecret}
	nodeA := models.Node{ID: nodeAID, TenantID: tenantID, Name: "node-a", PublicKey: "LOCAL_PUBLIC_KEY", OS: "linux", Arch: "amd64", Version: "test", Tags: []string{}, AgentTokenHash: "0000000000000000000000000000000000000000000000000000000000000001"}
	nodeB := models.Node{ID: nodeBID, TenantID: tenantID, Name: "node-b", PublicKey: "PEER_PUBLIC_KEY", PublicEndpoint: stringPointer("1.2.3.4:51820"), OS: "linux", Arch: "amd64", Version: "test", Tags: []string{}, AgentTokenHash: "0000000000000000000000000000000000000000000000000000000000000002"}
	memberA := models.NetworkMember{ID: uuid.MustParse("55555555-5555-5555-5555-555555555555"), NetworkID: networkID, NodeID: nodeAID, VirtualIP: "100.64.0.2", Role: "member", JoinedAt: joined}
	memberB := models.NetworkMember{ID: uuid.MustParse("66666666-6666-6666-6666-666666666666"), NetworkID: networkID, NodeID: nodeBID, VirtualIP: "100.64.0.3", Role: "member", JoinedAt: joined.Add(time.Second)}
	aclRule := models.ACLRule{ID: uuid.MustParse("77777777-7777-7777-7777-777777777777"), NetworkID: networkID, Src: "any", Dst: "any", Action: "allow", Protocol: "any", Ports: "any", Priority: 10}
	route := models.SubnetRoute{ID: uuid.MustParse("88888888-8888-8888-8888-888888888888"), NetworkID: networkID, NodeID: nodeBID, CIDR: "192.168.1.0/24", Enabled: true}
	for _, record := range []any{&tenant, &network, &nodeA, &nodeB, &memberA, &memberB, &aclRule, &route} {
		if err := handle.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	manager := New(handle, crypto, nil)
	first, err := manager.buildNodeConfig(context.Background(), handle, nodeA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.buildNodeConfig(context.Background(), handle, nodeA)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(first, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	again, err := json.MarshalIndent(second, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(again, '\n')) {
		t.Fatalf("config generation is not deterministic:\n%s\n%s", got, again)
	}
	path := filepath.Join("testdata", "node-config.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("config golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func stringPointer(value string) *string { return &value }

// Regression: a client lagging more than one version must be served the LATEST
// desired configuration (which advances it), never its own historical snapshot.
// Serving the snapshot leaves such a client applying a stale config, storing that
// stale version, and re-requesting it forever — a liveness bug (no convergence).
func TestDeliveryServesLatestForLaggingClient(t *testing.T) {
	handle, err := db.Open(appconfig.Database{
		Driver: "sqlite", DSN: "file:" + uuid.NewString() + "?mode=memory&cache=shared",
	}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := handle.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	crypto, err := cert.NewCryptoFromKey([]byte("delivery-key"))
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	nodeID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	hash := bytes.Repeat([]byte("a"), 64)
	for _, record := range []any{
		&models.Tenant{ID: tenantID, Name: "delivery"},
		&models.Node{ID: nodeID, TenantID: tenantID, Name: "node-a", PublicKey: "PUB", OS: "linux", Arch: "amd64", Version: "test", Tags: []string{}, AgentTokenHash: string(hash)},
	} {
		if err := handle.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	manager := New(handle, crypto, nil)
	ctx := context.Background()

	if _, err := manager.BumpForNode(ctx, nodeID, "initial"); err != nil {
		t.Fatal(err)
	}
	v1, err := manager.Latest(ctx, TargetNode, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.BumpForNode(ctx, nodeID, "change-2"); err != nil {
		t.Fatal(err)
	}
	v2, err := manager.Latest(ctx, TargetNode, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != v1.Version+1 {
		t.Fatalf("version bump = %d, want %d", v2.Version, v1.Version+1)
	}

	lagging, notModified, err := manager.Delivery(ctx, nodeID, v1.Version, false)
	if err != nil {
		t.Fatal(err)
	}
	if notModified {
		t.Fatal("lagging client must not be told not-modified")
	}
	if lagging.Version != v2.Version {
		t.Fatalf("lagging client got version %d, want latest %d (stale snapshot served)", lagging.Version, v2.Version)
	}

	if _, notModified, err := manager.Delivery(ctx, nodeID, v2.Version, false); err != nil || !notModified {
		t.Fatalf("up-to-date client: notModified=%v err=%v, want true/nil", notModified, err)
	}

	// A client AHEAD of the server (e.g. right after a rollback) must also receive
	// the latest config rather than an error for a version that no longer exists.
	ahead, _, err := manager.Delivery(ctx, nodeID, v2.Version+5, false)
	if err != nil {
		t.Fatalf("client ahead of server must not error: %v", err)
	}
	if ahead.Version != v2.Version {
		t.Fatalf("client ahead got version %d, want latest %d", ahead.Version, v2.Version)
	}
}
