package mesh

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeSTUN 是一台本地假 STUN 服务器：收到 Binding Request 就按 handler 回包。
type fakeSTUN struct {
	address  string
	requests chan []byte
}

func startFakeSTUN(t *testing.T, handler func(request []byte) []byte) *fakeSTUN {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("启动假 STUN 服务器失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	server := &fakeSTUN{address: conn.LocalAddr().String(), requests: make(chan []byte, 8)}
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, remote, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			request := make([]byte, n)
			copy(request, buffer[:n])
			select {
			case server.requests <- request:
			default:
			}
			response := handler(request)
			if response == nil {
				continue
			}
			_, _ = conn.WriteToUDP(response, remote)
		}
	}()
	return server
}

// stunCookieBytes 是魔 cookie 0x2112A442 的字节形式（网络序）。
var stunCookieBytes = []byte{0x21, 0x12, 0xA4, 0x42}

// buildXORMappedResponse 构造一条带 XOR-MAPPED-ADDRESS 的成功响应。
func buildXORMappedResponse(request []byte, address netip.Addr, port uint16) []byte {
	value := make([]byte, 8)
	value[0] = 0
	value[1] = 0x01 // IPv4
	binary.BigEndian.PutUint16(value[2:4], port^uint16(stunMagicCookie>>16))
	raw := address.As4()
	for index := range raw {
		value[4+index] = raw[index] ^ stunCookieBytes[index]
	}
	attribute := make([]byte, 0, 8)
	attribute = append(attribute, 0x00, 0x20) // XOR-MAPPED-ADDRESS
	attribute = append(attribute, 0x00, 0x08) // 长度 8
	attribute = append(attribute, value...)
	return stunResponse(request, stunBindingSuccess, attribute)
}

// buildErrorResponse 构造一条 STUN 错误响应。
func buildErrorResponse(request []byte) []byte {
	attribute := []byte{0x00, 0x09, 0x00, 0x01, 0x00} // ERROR-CODE
	return stunResponse(request, stunBindingError, attribute)
}

func stunResponse(request []byte, messageType uint16, attributes []byte) []byte {
	transactionID := make([]byte, stunTransactionIDSize)
	if len(request) >= stunHeaderSize {
		copy(transactionID, request[8:stunHeaderSize])
	}
	message := make([]byte, stunHeaderSize, stunHeaderSize+len(attributes))
	binary.BigEndian.PutUint16(message[0:2], messageType)
	binary.BigEndian.PutUint16(message[2:4], uint16(len(attributes)))
	binary.BigEndian.PutUint32(message[4:8], stunMagicCookie)
	copy(message[8:stunHeaderSize], transactionID)
	return append(message, attributes...)
}

func TestSTUNParsesXORMappedAddress(t *testing.T) {
	server := startFakeSTUN(t, func(request []byte) []byte {
		return buildXORMappedResponse(request, netip.MustParseAddr("203.0.113.7"), 54321)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	address, err := Discover(ctx, server.address, 0)
	if err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	if address != "203.0.113.7:54321" {
		t.Fatalf("Discover = %q，期望 %q", address, "203.0.113.7:54321")
	}
}

func TestSTUNUsesLocalWireGuardPort(t *testing.T) {
	server := startFakeSTUN(t, func(request []byte) []byte {
		return buildXORMappedResponse(request, netip.MustParseAddr("203.0.113.7"), 54321)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 服务器观察到的是临时端口，但对外暴露的候选必须是 WireGuard 的监听端口。
	address, err := Discover(ctx, server.address, 51820)
	if err != nil {
		t.Fatalf("Discover 失败: %v", err)
	}
	if address != "203.0.113.7:51820" {
		t.Fatalf("Discover = %q，期望 %q", address, "203.0.113.7:51820")
	}
}

func TestSTUNErrorResponseFails(t *testing.T) {
	server := startFakeSTUN(t, func(request []byte) []byte {
		return buildErrorResponse(request)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	address, err := discover(ctx, server.address, 0, 200*time.Millisecond, 2)
	if err == nil {
		t.Fatalf("STUN 错误响应应当返回错误，实际得到 %q", address)
	}
	if !strings.Contains(err.Error(), "错误响应") {
		t.Fatalf("错误信息应说明是 STUN 错误响应，实际: %v", err)
	}
}

func TestSTUNTimeoutFails(t *testing.T) {
	// 假服务器收到请求后不回包 → 必须超时返回错误。
	server := startFakeSTUN(t, func(request []byte) []byte { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	address, err := discover(ctx, server.address, 0, 100*time.Millisecond, 2)
	if err == nil {
		t.Fatalf("STUN 超时应当返回错误，实际得到 %q", address)
	}
}

func TestSTUNRejectsForeignResponse(t *testing.T) {
	// 事务 ID 不匹配的响应必须被拒绝（防止把别的会话的包当成探测结果）。
	server := startFakeSTUN(t, func(request []byte) []byte {
		response := buildXORMappedResponse(request, netip.MustParseAddr("203.0.113.7"), 54321)
		for index := 8; index < stunHeaderSize; index++ {
			response[index] ^= 0xff
		}
		return response
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := discover(ctx, server.address, 0, 100*time.Millisecond, 1); err == nil {
		t.Fatal("事务 ID 不匹配的响应应当返回错误")
	}
}

func TestSTUNServersFromEnvironment(t *testing.T) {
	t.Setenv(STUNServersEnv, " one.example:3478 , two.example:3478 ,, ")
	servers := Servers()
	if len(servers) != 2 || servers[0] != "one.example:3478" || servers[1] != "two.example:3478" {
		t.Fatalf("Servers() = %v，期望两个服务器", servers)
	}
	t.Setenv(STUNServersEnv, "")
	servers = Servers()
	if len(servers) != 1 || servers[0] != DefaultSTUNServer {
		t.Fatalf("Servers() = %v，期望默认服务器 %s", servers, DefaultSTUNServer)
	}
}

// TestSTUNRealServer 用真实公共 STUN 服务器验证一次解析（只打印映射地址，不含任何密钥）。
// 网络不可达时跳过，不影响其余单测。
func TestSTUNRealServer(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实 STUN 服务器测试（-short）")
	}
	server := strings.TrimSpace(os.Getenv("NEILICO_AGENT_STUN_SERVER"))
	if server == "" {
		server = DefaultSTUNServer
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	address, err := Discover(ctx, server, 0)
	if err != nil {
		t.Skipf("无法访问真实 STUN 服务器 %s（网络受限）: %v", server, err)
	}
	t.Logf("真实 STUN 服务器 %s 观察到的映射地址: %s", server, address)
	parsed, err := netip.ParseAddrPort(address)
	if err != nil {
		t.Fatalf("映射地址格式不合法 %q: %v", address, err)
	}
	if !parsed.Addr().IsGlobalUnicast() {
		t.Fatalf("映射地址不是可路由地址: %s", address)
	}
}
