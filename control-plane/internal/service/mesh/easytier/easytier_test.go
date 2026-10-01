package easytier

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"umpp/control-plane/internal/service/mesh"
)

func TestRenderExportGolden(t *testing.T) {
	network := mesh.Network{
		ID: "network-id", Name: "home", CIDR: "100.64.0.0/24", Secret: "NETWORK_SECRET",
		Peers: []mesh.Peer{
			{NodeID: "node-b", PublicKey: "PEER_B_PUBLIC", Endpoint: "", VirtualIP: "100.64.0.4", AllowedIPs: []string{"100.64.0.4/32"}},
			{NodeID: "node-c", PublicKey: "PEER_A_PUBLIC", Endpoint: "1.2.3.4:51820", VirtualIP: "100.64.0.3", AllowedIPs: []string{"100.64.0.3/32", "192.168.1.0/24"}},
		},
	}
	got, err := New().RenderExport(context.Background(), network)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "easytier.golden.toml")
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
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
