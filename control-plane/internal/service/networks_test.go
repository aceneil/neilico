package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service/cert"
)

func TestNetworkValidationAndConflicts(t *testing.T) {
	handle, crypto := newNetworkTestDB(t)
	service := NewNetworkService(handle, crypto)
	tenantID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "network-test"}).Error; err != nil {
		t.Fatal(err)
	}
	first, err := service.Create(context.Background(), tenantID, NetworkInput{Name: "home", CIDR: "10.1.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if first.NetworkSecret == "" || first.Secret == "" || first.Secret == first.NetworkSecret {
		t.Fatal("network secret was not returned exactly once and encrypted at rest")
	}
	if _, err := service.Create(context.Background(), tenantID, NetworkInput{Name: "home", CIDR: "10.2.0.0/24"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name error = %v, want conflict", err)
	}
	if _, err := service.Create(context.Background(), tenantID, NetworkInput{Name: "overlap", CIDR: "10.1.0.128/25"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlapping CIDR error = %v, want conflict", err)
	}
	if _, err := service.Create(context.Background(), tenantID, NetworkInput{Name: "huge", CIDR: "10.0.0.0/16"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized network error = %v, want invalid input", err)
	}
}

func TestVirtualIPAllocationIsConcurrentAndUnique(t *testing.T) {
	handle, crypto := newNetworkTestDB(t)
	service := NewNetworkService(handle, crypto)
	tenantID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "allocation-test"}).Error; err != nil {
		t.Fatal(err)
	}
	network, err := service.Create(context.Background(), tenantID, NetworkInput{Name: "mesh", CIDR: "100.64.9.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	nodeIDs := make([]uuid.UUID, count)
	for index := range nodeIDs {
		nodeIDs[index] = uuid.New()
		if err := handle.Create(&models.Node{
			ID: nodeIDs[index], TenantID: tenantID, Name: fmt.Sprintf("node-%02d", index),
			PublicKey: fmt.Sprintf("public-%02d", index), OS: "linux", Arch: "amd64", Version: "test",
			Tags: []string{}, AgentTokenHash: fmt.Sprintf("%064x", index+1),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	errs := make(chan error, count)
	for _, nodeID := range nodeIDs {
		wait.Add(1)
		go func(nodeID uuid.UUID) {
			defer wait.Done()
			_, err := service.AddMember(context.Background(), network.ID, &tenantID, NetworkMemberInput{NodeID: nodeID})
			errs <- err
		}(nodeID)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent AddMember: %v", err)
		}
	}
	var members []models.NetworkMember
	if err := handle.Where("network_id = ?", network.ID).Find(&members).Error; err != nil {
		t.Fatal(err)
	}
	if len(members) != count {
		t.Fatalf("member count = %d, want %d", len(members), count)
	}
	seenIP := make(map[netip.Addr]struct{}, count)
	for _, member := range members {
		addr, err := netip.ParseAddr(member.VirtualIP)
		if err != nil {
			t.Fatal(err)
		}
		if _, duplicate := seenIP[addr]; duplicate {
			t.Fatalf("duplicate allocated virtual IP %s", addr)
		}
		seenIP[addr] = struct{}{}
	}
	otherNodeID := uuid.New()
	if err := handle.Create(&models.Node{
		ID: otherNodeID, TenantID: tenantID, Name: "other", PublicKey: "other-public",
		OS: "linux", Arch: "amd64", Version: "test", Tags: []string{},
		AgentTokenHash: fmt.Sprintf("%064x", 1000),
	}).Error; err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.AddMember(context.Background(), network.ID, &tenantID, NetworkMemberInput{
		NodeID: otherNodeID, VirtualIP: members[0].VirtualIP,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate requested virtual IP result = %#v, %v; want conflict", duplicate, err)
	}
}

func TestVirtualIPSkipsReservedAddresses(t *testing.T) {
	handle, crypto := newNetworkTestDB(t)
	service := NewNetworkService(handle, crypto)
	tenantID := uuid.New()
	if err := handle.Create(&models.Tenant{ID: tenantID, Name: "reserved-test"}).Error; err != nil {
		t.Fatal(err)
	}
	network, err := service.Create(context.Background(), tenantID, NetworkInput{Name: "mesh", CIDR: "100.64.8.0/29"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	if err := handle.Create(&models.Node{
		ID: nodeID, TenantID: tenantID, Name: "first", PublicKey: "public", OS: "linux", Arch: "amd64",
		Version: "test", Tags: []string{}, AgentTokenHash: fmt.Sprintf("%064x", 99),
	}).Error; err != nil {
		t.Fatal(err)
	}
	member, err := service.AddMember(context.Background(), network.ID, &tenantID, NetworkMemberInput{NodeID: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if member.VirtualIP != "100.64.8.2" {
		t.Fatalf("first virtual IP = %q, want 100.64.8.2", member.VirtualIP)
	}
}

func newNetworkTestDB(t *testing.T) (*gorm.DB, *cert.Crypto) {
	t.Helper()
	handle, err := db.Open(config.Database{
		Driver: "sqlite",
		DSN:    "file:" + uuid.NewString() + "?mode=memory&cache=shared",
	}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	crypto, err := cert.NewCryptoFromKey([]byte("network-test-encryption-key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := handle.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return handle, crypto
}
