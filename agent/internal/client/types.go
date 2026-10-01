package client

import "encoding/json"

type RegisterRequest struct {
	Name    string   `json:"name"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Version string   `json:"version"`
	Tags    []string `json:"tags"`
}

type RegisterResponse struct {
	NodeID     string `json:"node_id"`
	AgentToken string `json:"agent_token"`
	TenantID   string `json:"tenant_id"`
	Status     string `json:"status"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

type HeartbeatRequest struct {
	Version string `json:"version"`
}

type HeartbeatResponse struct {
	OK                   bool           `json:"ok"`
	NextHeartbeatSeconds int            `json:"next_heartbeat_seconds"`
	ServerTime           string         `json:"server_time"`
	Node                 map[string]any `json:"node"`
}

type NetworkReportRequest struct {
	PublicEndpoint string `json:"public_endpoint"`
}

type NodeIdentity struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	VirtualIP      string `json:"virtual_ip"`
	PublicEndpoint string `json:"public_endpoint"`
}

type Peer struct {
	NodeID     string   `json:"node_id"`
	PublicKey  string   `json:"public_key"`
	Endpoint   string   `json:"endpoint"`
	AllowedIPs []string `json:"allowed_ips"`
	VirtualIP  string   `json:"virtual_ip"`
}

type Network struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CIDR          string `json:"cidr"`
	NetworkSecret string `json:"network_secret,omitempty"`
	Peers         []Peer `json:"peers"`
}

type Route struct {
	ID      string `json:"id"`
	NodeID  string `json:"node_id"`
	CIDR    string `json:"cidr"`
	Enabled bool   `json:"enabled"`
}

type ProxyRule struct {
	ID            string          `json:"id"`
	DomainID      string          `json:"domain_id"`
	Path          string          `json:"path"`
	TargetType    string          `json:"target_type"`
	Target        string          `json:"target"`
	AccessControl json.RawMessage `json:"access_control"`
	Enabled       bool            `json:"enabled"`
}

type Delivery struct {
	Version         int               `json:"version"`
	Node            NodeIdentity      `json:"node"`
	Network         *Network          `json:"network"`
	ProxyRules      []ProxyRule       `json:"proxy_rules"`
	ACL             []json.RawMessage `json:"acl"`
	Routes          []Route           `json:"routes"`
	PolicyFiltered  bool              `json:"policy_filtered"`
	WireGuardConfig string            `json:"wireguard_config"`
}

type ConfigResult struct {
	Delivery    Delivery
	NotModified bool
	Version     int
}

type TrafficInput struct {
	Direction string `json:"direction"`
	Bytes     int64  `json:"bytes"`
	Protocol  string `json:"protocol"`
	Peer      string `json:"peer"`
}
