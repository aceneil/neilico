package wireguard

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"umpp/control-plane/internal/service/mesh"
	"umpp/control-plane/internal/service/mesh/easytier"
)

const (
	Kind        = "wireguard"
	DefaultMTU  = 1420
	DefaultPort = 51820
)

type Provider struct{}

func New() *Provider { return &Provider{} }

func GenerateKeyPair() (privateKey, publicKey string, err error) {
	private, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", "", fmt.Errorf("generate WireGuard private key: %w", err)
	}
	return private.String(), private.PublicKey().String(), nil
}

func (*Provider) Kind() string { return Kind }

func (*Provider) RenderNodeConfig(_ context.Context, node mesh.Node) ([]byte, error) {
	if node.PrivateKey == "" || node.PublicKey == "" || node.VirtualIP == "" {
		return nil, fmt.Errorf("wireguard node private_key, public_key, and virtual_ip are required")
	}
	listenPort := node.ListenPort
	if listenPort == 0 {
		listenPort = DefaultPort
	}
	peers := append([]mesh.Peer(nil), node.Network.Peers...)
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].PublicKey == peers[j].PublicKey {
			return peers[i].NodeID < peers[j].NodeID
		}
		return peers[i].PublicKey < peers[j].PublicKey
	})
	var output strings.Builder
	output.WriteString("# UMPP WireGuard configuration\n")
	fmt.Fprintf(&output, "# policy_filtered: %t\n", node.PolicyFiltered)
	output.WriteString("[Interface]\n")
	fmt.Fprintf(&output, "PrivateKey = %s\n", node.PrivateKey)
	fmt.Fprintf(&output, "Address = %s/32\n", node.VirtualIP)
	fmt.Fprintf(&output, "ListenPort = %d\n", listenPort)
	fmt.Fprintf(&output, "MTU = %d\n", DefaultMTU)
	for _, peer := range peers {
		output.WriteString("\n[Peer]\n")
		fmt.Fprintf(&output, "PublicKey = %s\n", peer.PublicKey)
		fmt.Fprintf(&output, "Endpoint = %s\n", strings.TrimSpace(peer.Endpoint))
		fmt.Fprintf(&output, "AllowedIPs = %s\n", strings.Join(peer.AllowedIPs, ", "))
		output.WriteString("PersistentKeepalive = 25\n")
	}
	return []byte(output.String()), nil
}

func (*Provider) RenderExport(ctx context.Context, network mesh.Network) ([]byte, error) {
	return easytier.New().RenderExport(ctx, network)
}
