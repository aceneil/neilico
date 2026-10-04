package mesh

import (
	"net/netip"
	"strings"
	"testing"

	"neilico/agent/internal/client"
)

func mustPrefix(t *testing.T, value string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		t.Fatalf("bad prefix %q: %v", value, err)
	}
	return prefix
}

const sampleConfigText = `# NEILICO WireGuard configuration
[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 100.64.0.3/32
ListenPort = 51820
MTU = 1420

[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
Endpoint = 47.242.186.221:51820
AllowedIPs = 100.64.0.2/32
PresharedKey = CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=
PersistentKeepalive = 25
`

// 同内网的对端应改用内网地址——实测中公网出口（云代理 IP）互相拨不通，
// 而同内网的 192.168.123.x 是直连可达的，这是 mesh 能否连上的关键。
func TestPreferLANEndpointsRewritesSameSubnetPeer(t *testing.T) {
	config := Config{
		Version:         1,
		ListenPort:      51820,
		WireGuardConfig: sampleConfigText,
		Peers: []client.Peer{{
			NodeID:         "peer-1",
			PublicKey:      "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			Endpoint:       "47.242.186.221:51820",
			AllowedIPs:     []string{"100.64.0.2/32"},
			VirtualIP:      "100.64.0.2",
			LocalAddresses: []string{"192.168.123.106/24"},
			ListenPort:     51820,
		}},
	}
	notes := PreferLANEndpoints(&config, []netip.Prefix{mustPrefix(t, "192.168.123.90/24")})
	if len(notes) != 1 {
		t.Fatalf("应替换 1 个对端，实际 %d：%v", len(notes), notes)
	}
	want := "192.168.123.106:51820"
	if config.Peers[0].Endpoint != want {
		t.Fatalf("结构体 endpoint = %q，期望 %q", config.Peers[0].Endpoint, want)
	}
	if strings.Count(config.WireGuardConfig, "Endpoint = "+want) != 1 {
		t.Fatalf("配置文本里应恰好有一行内网 Endpoint，实际：\n%s", config.WireGuardConfig)
	}
	if strings.Contains(config.WireGuardConfig, "47.242.186.221:51820") {
		t.Fatal("原公网 endpoint 应被替换掉")
	}
	parsed, err := parseWireGuardConfig(config.WireGuardConfig)
	if err != nil {
		t.Fatalf("替换后配置应仍可解析: %v", err)
	}
	if len(parsed.peers) != 1 || parsed.peers[0].endpoint != want {
		t.Fatalf("解析出的 endpoint = %+v，期望 %q", parsed.peers, want)
	}
}

// 跨网络的对端不应被改动，保持原公网地址行为。
func TestPreferLANEndpointsKeepsForeignPeer(t *testing.T) {
	config := Config{
		ListenPort:      51820,
		WireGuardConfig: sampleConfigText,
		Peers: []client.Peer{{
			PublicKey:      "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			Endpoint:       "47.242.186.221:51820",
			LocalAddresses: []string{"10.9.9.9/24"},
		}},
	}
	if notes := PreferLANEndpoints(&config, []netip.Prefix{mustPrefix(t, "192.168.123.90/24")}); len(notes) != 0 {
		t.Fatalf("不该替换跨网对端：%v", notes)
	}
	if config.Peers[0].Endpoint != "47.242.186.221:51820" || !strings.Contains(config.WireGuardConfig, "47.242.186.221:51820") {
		t.Fatal("跨网对端应保持原样")
	}
}

// 对端没有 Endpoint 行（未上报公网地址）时，应补上内网 Endpoint 行。
func TestPreferLANEndpointsInsertsMissingEndpointLine(t *testing.T) {
	text := strings.Replace(sampleConfigText, "Endpoint = 47.242.186.221:51820\n", "", 1)
	config := Config{
		ListenPort:      51820,
		WireGuardConfig: text,
		Peers: []client.Peer{{
			PublicKey:      "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			LocalAddresses: []string{"192.168.123.106"},
		}},
	}
	PreferLANEndpoints(&config, []netip.Prefix{mustPrefix(t, "192.168.123.90/24")})
	if strings.Count(config.WireGuardConfig, "Endpoint = 192.168.123.106:51820") != 1 {
		t.Fatalf("应补一行内网 Endpoint，实际：\n%s", config.WireGuardConfig)
	}
	if _, err := parseWireGuardConfig(config.WireGuardConfig); err != nil {
		t.Fatalf("补行后应可解析: %v", err)
	}
}

// 实测教训：宿主机上会有 10 个左右 docker 网桥 + 隧道接口自己的 VIP，
// 它们把上报条数配额吃光，真正的 192.168.123.90/24 被挤掉 → 同内网直连失效。
func TestLocalPrefixesSkipsVirtualInterfacesAndMeshRange(t *testing.T) {
	prefixes := LocalPrefixes("wg0")
	for _, prefix := range prefixes {
		address := prefix.Addr()
		if address.IsLoopback() || address.IsLinkLocalUnicast() || !address.IsGlobalUnicast() {
			t.Fatalf("不应包含不可用地址: %s", prefix)
		}
		if !address.Is4() {
			t.Fatalf("当前只应收集 IPv4：%s", prefix)
		}
		if meshRange.Contains(address) {
			t.Fatalf("不应包含 NEILICO 虚拟 IP 段（100.64.0.0/10）: %s", prefix)
		}
		if address.IsPrivate() && strings.HasPrefix(address.String(), "172.") {
			t.Fatalf("不应包含 docker 网桥地址: %s", prefix)
		}
	}
}

// 内网 endpoint 的端口必须是**对端自己的**监听端口，不能用本机的。
func TestPreferLANEndpointsUsesPeerListenPort(t *testing.T) {
	config := Config{
		ListenPort:      51820, // 本机端口
		WireGuardConfig: sampleConfigText,
		Peers: []client.Peer{{
			PublicKey:      "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			LocalAddresses: []string{"192.168.123.106/24"},
			ListenPort:     51999, // 对端自己的端口
		}},
	}
	PreferLANEndpoints(&config, []netip.Prefix{mustPrefix(t, "192.168.123.90/24")})
	if want := "192.168.123.106:51999"; config.Peers[0].Endpoint != want {
		t.Fatalf("endpoint = %q，期望 %q（要用对端自己的端口）", config.Peers[0].Endpoint, want)
	}
	if !strings.Contains(config.WireGuardConfig, "Endpoint = 192.168.123.106:51999") {
		t.Fatal("配置文本里也应是带对端端口的内网地址")
	}
}
