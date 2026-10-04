package wireguard

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"neilico/control-plane/internal/service/mesh"
	"neilico/control-plane/internal/service/mesh/easytier"
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
	output.WriteString("# NEILICO WireGuard configuration\n")
	fmt.Fprintf(&output, "# policy_filtered: %t\n", node.PolicyFiltered)
	output.WriteString("[Interface]\n")
	fmt.Fprintf(&output, "PrivateKey = %s\n", node.PrivateKey)
	fmt.Fprintf(&output, "Address = %s/32\n", node.VirtualIP)
	fmt.Fprintf(&output, "ListenPort = %d\n", listenPort)
	fmt.Fprintf(&output, "MTU = %d\n", DefaultMTU)
	for _, peer := range peers {
		output.WriteString("\n[Peer]\n")
		fmt.Fprintf(&output, "PublicKey = %s\n", peer.PublicKey)
		// 对端还没上报 endpoint 时**不要写空行**：`Endpoint = ` 会让解析器再报一个错。
		// 没有 endpoint 的 peer 仍然可用——只要对端主动发起，本端就会学到它的地址（WireGuard roaming）。
		if endpoint := strings.TrimSpace(peer.Endpoint); endpoint != "" {
			fmt.Fprintf(&output, "Endpoint = %s\n", endpoint)
		}
		fmt.Fprintf(&output, "AllowedIPs = %s\n", strings.Join(peer.AllowedIPs, ", "))
		// NormalizeKey：兜底把历史遗留的 base64url 预共享密钥转成标准 base64，
		// 否则 wg 会拒绝整份配置（见 mesh.NormalizeKey 的注释）。
		if psk := mesh.NormalizeKey(node.Network.PresharedKey); psk != "" {
			fmt.Fprintf(&output, "PresharedKey = %s\n", psk)
		}
		output.WriteString("PersistentKeepalive = 25\n")
	}
	return []byte(output.String()), nil
}

func (*Provider) RenderExport(ctx context.Context, network mesh.Network) ([]byte, error) {
	return easytier.New().RenderExport(ctx, network)
}
