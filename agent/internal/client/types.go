package client

import (
	"encoding/json"
	"neilico/agent/internal/netinfo"

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

// Candidate 是本机的一个候选地址（ip:port），供对端挑一条能走通的路径。
//
// Kind 目前的取值：
//   - "lan"：本机内网地址（直连可达，优先）；
//   - "public"：公网出口地址（ipify 探测，出口 IP ≠ 端口映射，仅作兜底）；
//   - "stun"：STUN 服务器观察到的公网映射地址（打洞最需要的那个）。
type Candidate struct {
	Kind string `json:"kind"`
	Addr string `json:"addr"`
}

type HeartbeatRequest struct {
	Version      string                     `json:"version"`
	Capabilities *capabilities.Capabilities `json:"capabilities,omitempty"`
	// Network 描述本机的运行环境与默认网关，用于控制面判断「是否同一实体局域网」。
	Network *netinfo.Info `json:"network,omitempty"`
	// Candidates 是本机的候选地址，随心跳上报给控制面并下发给对端。
	// 字段是新增的，老版本控制面会直接忽略（omitempty 保持向后兼容）。
	Candidates []Candidate `json:"candidates,omitempty"`
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
	// Candidates 是对端上报的候选地址（stun / public / lan），供本机打洞选路。
	Candidates []Candidate `json:"candidates,omitempty"`
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
