package wireguard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"umpp/control-plane/internal/service/mesh"
)

func TestRenderNodeConfigGolden(t *testing.T) {
	node := mesh.Node{
		Name: "node-a", PrivateKey: "LOCAL_PRIVATE_KEY", PublicKey: "LOCAL_PUBLIC_KEY",
		VirtualIP: "100.64.0.2", ListenPort: 51820, PolicyFiltered: true,
		Network: mesh.Network{
			ID: "network-id", Name: "home", CIDR: "100.64.0.0/24", PresharedKey: "NETWORK_PRESHARED_KEY",
			Peers: []mesh.Peer{
				{
					NodeID: "node-b", PublicKey: "PEER_B_PUBLIC", Endpoint: "",
					VirtualIP: "100.64.0.4", AllowedIPs: []string{"100.64.0.4/32"},
				},
				{
					NodeID: "node-c", PublicKey: "PEER_A_PUBLIC", Endpoint: "1.2.3.4:51820",
					VirtualIP: "100.64.0.3", AllowedIPs: []string{"192.168.1.0/24", "100.64.0.3/32"},
				},
			},
		},
	}
	// The renderer is deterministic after ordering by public key.
	node.Network.Peers[1].AllowedIPs = []string{"100.64.0.3/32", "192.168.1.0/24"}
	got, err := New().RenderNodeConfig(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, filepath.Join("testdata", "wireguard.golden.conf"), got)
	if again, err := New().RenderNodeConfig(context.Background(), node); err != nil || !bytes.Equal(got, again) {
		t.Fatalf("render was not deterministic: %v\n%s", err, again)
	}
	if bytes.Count(got, []byte("PresharedKey = NETWORK_PRESHARED_KEY")) != len(node.Network.Peers) {
		t.Fatalf("not every peer received the same PSK:\n%s", got)
	}
	other := node
	other.Network.PresharedKey = "OTHER_NETWORK_PRESHARED_KEY"
	otherOutput, err := New().RenderNodeConfig(context.Background(), other)
	if err != nil || bytes.Contains(otherOutput, []byte("PresharedKey = NETWORK_PRESHARED_KEY\n")) {
		t.Fatalf("different network reused PSK: %v\n%s", err, otherOutput)
	}
}

func assertGolden(t *testing.T, path string, got []byte) {
	t.Helper()
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
