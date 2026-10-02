package mesh

import (
	"context"

	"neilico/agent/internal/client"
	"neilico/agent/internal/route"
)

type Applier interface {
	Apply(context.Context, Config) error
	Cleanup(context.Context) error
}

type Config struct {
	Version         int
	NodeID          string
	WireGuardConfig string
	Interface       string
	MTU             int
	ListenPort      int
	Peers           []client.Peer
	Routes          []client.Route
	NetworkSecret   string
}

func LocalRoutes(nodeID string, routes []client.Route) []route.Route {
	result := make([]route.Route, 0, len(routes))
	for _, item := range routes {
		if item.Enabled && item.NodeID == nodeID {
			result = append(result, route.Route{CIDR: item.CIDR})
		}
	}
	return result
}
