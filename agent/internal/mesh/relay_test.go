package mesh

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"neilico/agent/internal/client"
)

// --- 帧编解码 ---------------------------------------------------------------

func TestRelayFrameCodecRoundTrip(t *testing.T) {
	nodeID, err := parseRelayNodeID("11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatalf("解析 node id 失败：%v", err)
	}
	payload := []byte{0x01, 0x02, 0x03}
	encoded := encodeRelayFrame(relayFrameData, nodeID, payload)
	if len(encoded) != relayHeaderSize+len(payload) {
		t.Fatalf("编码长度 = %d，期望 %d", len(encoded), relayHeaderSize+len(payload))
	}
	frameType, decodedID, decodedPayload, err := decodeRelayFrame(encoded)
	if err != nil {
		t.Fatalf("解析帧失败：%v", err)
	}
	if frameType != relayFrameData {
		t.Fatalf("帧类型 = %d，期望 %d", frameType, relayFrameData)
	}
	if decodedID != nodeID {
		t.Fatalf("node id 不一致：%v != %v", decodedID, nodeID)
	}
	if string(decodedPayload) != string(payload) {
		t.Fatalf("载荷被改动了：%v", decodedPayload)
	}
	if _, _, _, err := decodeRelayFrame([]byte{1, 2}); err == nil {
		t.Fatal("短帧应该报错")
	}
}

func TestRelayTokenCodecRoundTrip(t *testing.T) {
	payload := encodeRelayToken("agent-token")
	token, err := decodeRelayToken(payload)
	if err != nil {
		t.Fatalf("解析 token 失败：%v", err)
	}
	if token != "agent-token" {
		t.Fatalf("token = %q", token)
	}
	if _, err := decodeRelayToken([]byte{0xff, 0xff}); err == nil {
		t.Fatal("越界的 token 长度应该报错")
	}
}

func TestRelayDataPayloadRoundTrip(t *testing.T) {
	destination, err := parseRelayNodeID("99999999-8888-7777-6666-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte{0xde, 0xad, 0xbe, 0xef}
	payload := encodeRelayData(destination, packet)
	got, decoded, err := decodeRelayData(payload)
	if err != nil {
		t.Fatalf("解析 DATA 载荷失败：%v", err)
	}
	if got != destination {
		t.Fatalf("目标 node id = %v", got)
	}
	if string(decoded) != string(packet) {
		t.Fatalf("原始报文被改动了：%v", decoded)
	}
}

func TestParseRelayNodeID(t *testing.T) {
	if _, err := parseRelayNodeID("not-a-uuid"); err == nil {
		t.Fatal("非法 UUID 应该报错")
	}
	if _, err := parseRelayNodeID("11111111222233334444555555555555"); err == nil {
		t.Fatal("缺少分隔符的 UUID 应该报错")
	}
	first, err := parseRelayNodeID("11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseRelayNodeID("{11111111-2222-3333-4444-555555555555}")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("带花括号与不带的 UUID 应该一致：%v != %v", first, second)
	}
}

// --- 本地代理 ---------------------------------------------------------------

// 本地代理收发：用两个本地 UDP socket 模拟「内核 → 代理 → 中继 → 对端代理 → 内核」。
func TestLocalProxyRoundTrip(t *testing.T) {
	first, err := newLocalProxy(0)
	if err != nil {
		t.Fatalf("创建本地代理失败：%v", err)
	}
	defer first.Close()
	second, err := newLocalProxy(0)
	if err != nil {
		t.Fatalf("创建本地代理失败：%v", err)
	}
	defer second.Close()

	// 模拟中继：把从第一个代理收到的报文转发给第二个代理。
	forwarded := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, 2048)
		for {
			length, _, err := first.conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			packet := make([]byte, length)
			copy(packet, buffer[:length])
			forwarded <- packet
		}
	}()

	// 模拟内核 WireGuard：往代理端口发一个包。
	kernel, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer kernel.Close()
	payload := []byte("wireguard-packet")
	if _, err := kernel.WriteToUDP(payload, first.local); err != nil {
		t.Fatalf("模拟内核发包失败：%v", err)
	}
	select {
	case got := <-forwarded:
		if string(got) != string(payload) {
			t.Fatalf("代理读到的报文 = %q，期望 %q", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("本地代理没有读到报文")
	}

	// 中继回来的报文要注入对端代理（源地址必须是对端代理自己，否则内核 WireGuard 会丢）。
	received := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, 2048)
		length, addr, err := second.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		packet := make([]byte, length)
		copy(packet, buffer[:length])
		if addr.String() != second.Address() {
			t.Errorf("注入报文的源地址 = %s，期望 %s", addr, second.Address())
		}
		received <- packet
	}()
	if err := second.Deliver(payload); err != nil {
		t.Fatalf("注入报文失败：%v", err)
	}
	select {
	case got := <-received:
		if string(got) != string(payload) {
			t.Fatalf("对端代理收到的报文 = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("对端代理没有收到报文")
	}
}

// --- 中继管理器 -------------------------------------------------------------

// fakeRelay 是一个最小的中继服务端（只回 REGISTERED）。
func fakeRelay(t *testing.T) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			length, addr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			frameType, nodeID, _, err := decodeRelayFrame(buffer[:length])
			if err != nil {
				continue
			}
			if frameType == relayFrameRegister {
				_, _ = conn.WriteToUDP(encodeRelayFrame(relayFrameRegistered, nodeID, []byte("relay.example.com:51821")), addr)
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

type endpointRecorder struct {
	mutex     sync.Mutex
	endpoints []string
}

func (r *endpointRecorder) set(_ context.Context, _ string, endpoint string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.endpoints = append(r.endpoints, endpoint)
	return nil
}

func (r *endpointRecorder) all() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string{}, r.endpoints...)
}

// 直连可用（最近有握手）时，中继不得覆盖打洞器/控制面设好的直连 endpoint。
func TestRelayManagerKeepsWorkingDirectEndpoint(t *testing.T) {
	relayAddress := fakeRelay(t)
	recorder := &endpointRecorder{}
	manager := NewRelayManager(RelayOptions{
		NodeID:           "11111111-2222-3333-4444-555555555555",
		Token:            "token",
		EvaluateInterval: 10 * time.Millisecond,
		FailoverDelay:    20 * time.Millisecond,
		SetEndpoint:      recorder.set,
		PeerState: func(context.Context, string) (string, time.Time, error) {
			// 直连刚握手成功 → 走直连。
			return "1.2.3.4:51820", time.Now(), nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	peers := []RelayPeer{{NodeID: "99999999-8888-7777-6666-555555555555", PublicKey: "peer-key", Endpoint: relayAddress.String()}}
	if err := manager.Apply(ctx, peers); err != nil {
		t.Fatalf("应用中继配置失败：%v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if endpoints := recorder.all(); len(endpoints) != 0 {
		t.Fatalf("直连可用时不应改 endpoint，实际改了：%v", endpoints)
	}
}

// 直连不通（一直没有握手）时，中继要接管 endpoint（兜底）。
func TestRelayManagerTakesOverWhenDirectPathFails(t *testing.T) {
	relayAddress := fakeRelay(t)
	recorder := &endpointRecorder{}
	manager := NewRelayManager(RelayOptions{
		NodeID:           "11111111-2222-3333-4444-555555555555",
		Token:            "token",
		EvaluateInterval: 10 * time.Millisecond,
		FailoverDelay:    20 * time.Millisecond,
		SetEndpoint:      recorder.set,
		PeerState: func(context.Context, string) (string, time.Time, error) {
			// 从来没握手过 → 直连不通。
			return "1.2.3.4:51820", time.Time{}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	peers := []RelayPeer{{NodeID: "99999999-8888-7777-6666-555555555555", PublicKey: "peer-key", Endpoint: relayAddress.String()}}
	if err := manager.Apply(ctx, peers); err != nil {
		t.Fatalf("应用中继配置失败：%v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		endpoints := recorder.all()
		if len(endpoints) > 0 {
			if !strings.HasPrefix(endpoints[0], "127.0.0.1:") {
				t.Fatalf("接管后的 endpoint = %q，期望本地回环地址", endpoints[0])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("直连不通时中继没有接管 endpoint")
}

// 控制面没下发 relay_endpoint 的对端不走中继（行为与中继上线前完全一致）。
func TestRelayPeersFromPeers(t *testing.T) {
	peers := []client.Peer{
		{NodeID: "a", PublicKey: "key-a"},
		{NodeID: "b", PublicKey: "key-b", RelayEndpoint: "relay.example.com:51821", RelayNodeID: "node-b"},
		{NodeID: "c", PublicKey: "key-c", RelayEndpoint: "relay.example.com:51821"},
	}
	result := RelayPeersFromPeers(peers)
	if len(result) != 1 {
		t.Fatalf("走中继的对端 = %+v，期望 1 个", result)
	}
	if result[0].NodeID != "node-b" || result[0].PublicKey != "key-b" || result[0].Endpoint != "relay.example.com:51821" {
		t.Fatalf("走中继的对端内容不对：%+v", result[0])
	}
}
