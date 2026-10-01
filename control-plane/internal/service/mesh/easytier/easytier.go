package easytier

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"umpp/control-plane/internal/service/mesh"
)

const Kind = "easytier-config-export"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (*Provider) Kind() string { return Kind }

func (*Provider) RenderNodeConfig(_ context.Context, node mesh.Node) ([]byte, error) {
	return nil, fmt.Errorf("easytier provider is export-only")
}

func (*Provider) RenderExport(_ context.Context, network mesh.Network) ([]byte, error) {
	if network.Name == "" || network.Secret == "" {
		return nil, fmt.Errorf("easytier network name and secret are required")
	}
	peers := append([]mesh.Peer(nil), network.Peers...)
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].PublicKey == peers[j].PublicKey {
			return peers[i].NodeID < peers[j].NodeID
		}
		return peers[i].PublicKey < peers[j].PublicKey
	})
	var output strings.Builder
	output.WriteString("# UMPP EasyTier configuration export (export only; not wired to runtime)\n")
	fmt.Fprintf(&output, "network_name = %s\n", quote(network.Name))
	fmt.Fprintf(&output, "network_secret = %s\n", quote(network.Secret))
	output.WriteString("peers = [\n")
	for _, peer := range peers {
		output.WriteString("  {\n")
		fmt.Fprintf(&output, "    public_key = %s,\n", quote(peer.PublicKey))
		fmt.Fprintf(&output, "    endpoint = %s,\n", quote(peer.Endpoint))
		output.WriteString("    allowed_ips = [")
		for index, allowedIP := range peer.AllowedIPs {
			if index > 0 {
				output.WriteString(", ")
			}
			output.WriteString(quote(allowedIP))
		}
		output.WriteString("],\n")
		output.WriteString("  },\n")
	}
	output.WriteString("]\n")
	return []byte(output.String()), nil
}

func quote(value string) string {
	return strconv.Quote(value)
}
