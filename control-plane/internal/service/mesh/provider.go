package mesh

import "context"

type Provider interface {
	Kind() string
	RenderNodeConfig(ctx context.Context, node Node) ([]byte, error)
	RenderExport(ctx context.Context, network Network) ([]byte, error)
}

type Node struct {
	Name           string
	PrivateKey     string
	PublicKey      string
	VirtualIP      string
	PublicEndpoint string
	ListenPort     int
	Network        Network
	PolicyFiltered bool
}

type Peer struct {
	NodeID     string
	PublicKey  string
	Endpoint   string
	VirtualIP  string
	AllowedIPs []string
}

type Network struct {
	ID           string
	Name         string
	CIDR         string
	Secret       string
	PresharedKey string
	Peers        []Peer
}
