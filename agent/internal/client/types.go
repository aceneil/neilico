package client

import (
	"encoding/json"

	"neilico/control-plane/pkg/capabilities"
)

type RegisterRequest struct {
	Name         string                     `json:"name"`
	OS           string                     `json:"os"`
	Arch         string                     `json:"arch"`
	Version      string                     `json:"version"`
	Tags         []string                   `json:"tags"`
	Capabilities *capabilities.Capabilities `json:"capabilities,omitempty"`
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
	Version      string                     `json:"version"`
	Capabilities *capabilities.Capabilities `json:"capabilities,omitempty"`
}

type HeartbeatResponse struct {
	OK                   bool           `json:"ok"`
	NextHeartbeatSeconds int            `json:"next_heartbeat_seconds"`
	ServerTime           string         `json:"server_time"`
	Node                 map[string]any `json:"node"`
}

type NetworkReportRequest struct {
	PublicEndpoint string `json:"public_endpoint"`
	// LocalAddresses 是本机内网地址（CIDR 形式，如 192.168.50.10/24）。
	// 控制面把它转给同网络的对端，让"同一内网"的设备直接用内网地址建隧道——
	// 公网地址在 NAT/代理出口后常常互相拨不通（实测）。
	LocalAddresses []string `json:"local_addresses,omitempty"`
	// ListenPort 是本机 WireGuard 监听端口；对端要用它拼内网 endpoint。
	ListenPort int `json:"listen_port,omitempty"`
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
	// LocalAddresses 是对端上报的内网地址；与本机同一内网时优先用它当 endpoint。
	LocalAddresses []string `json:"local_addresses,omitempty"`
	// ListenPort 是对端自己的监听端口（与本机可能不同）。
	ListenPort int `json:"listen_port,omitempty"`
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
