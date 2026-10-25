package relay

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- 测试替身 ---------------------------------------------------------------

type fakeAuthenticator struct {
	mutex sync.Mutex
	seen  map[uuid.UUID]string
	fail  bool
}

func newFakeAuthenticator() *fakeAuthenticator {
	return &fakeAuthenticator{seen: map[uuid.UUID]string{}}
}

func (f *fakeAuthenticator) AuthenticateRelay(_ context.Context, nodeID uuid.UUID, token string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.fail {
		return errors.New("token mismatch")
	}
	f.seen[nodeID] = token
	if token != "valid-token" {
		return errors.New("token mismatch")
	}
	return nil
}

type fakeMetrics struct {
	mutex    sync.Mutex
	packets  map[string]int
	bytes    int
	sessions int
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{packets: map[string]int{}}
}

func (f *fakeMetrics) ObserveRelayPacket(direction string, count int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.packets[direction] += count
}

func (f *fakeMetrics) ObserveRelayBytes(count int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.bytes += count
}

func (f *fakeMetrics) SetRelaySessions(count int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.sessions = count
}

func (f *fakeMetrics) packetCount(direction string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.packets[direction]
}

type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.now
}

func (f *fakeClock) Advance(duration time.Duration) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.now = f.now.Add(duration)
}

// testClient 是一个最小的中继客户端（单测用）。
type testClient struct {
	conn *net.UDPConn
	id   uuid.UUID
}

func newTestClient(t *testing.T, id uuid.UUID) *testClient {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("监听本地 UDP 失败：%v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &testClient{conn: conn, id: id}
}

func (c *testClient) send(t *testing.T, server *net.UDPAddr, frame Frame) {
	t.Helper()
	if _, err := c.conn.WriteToUDP(frame.Encode(), server); err != nil {
		t.Fatalf("发送帧失败：%v", err)
	}
}

func (c *testClient) receive(t *testing.T) Frame {
	t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("设置读超时失败：%v", err)
	}
	buffer := make([]byte, 2048)
	length, _, err := c.conn.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("读取帧失败：%v", err)
	}
	frame, err := DecodeFrame(buffer[:length])
	if err != nil {
		t.Fatalf("解析帧失败：%v", err)
	}
	return frame
}

func (c *testClient) expectNothing(t *testing.T) {
	t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("设置读超时失败：%v", err)
	}
	buffer := make([]byte, 2048)
	if _, _, err := c.conn.ReadFromUDP(buffer); err == nil {
		t.Fatal("不应该收到任何帧")
	}
}

// startServer 起一个跑在本地随机端口上的中继服务端。
func startServer(t *testing.T, options ServerOptions, authenticator Authenticator, metrics Metrics) (*Server, *net.UDPAddr) {
	t.Helper()
	if options.Listen == "" {
		options.Listen = "127.0.0.1:0"
	}
	server := New(options, authenticator, metrics)
	conn, err := server.Listen()
	if err != nil {
		t.Fatalf("中继监听失败：%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.ServeConn(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		// 关闭连接才能让阻塞中的 ReadFromUDP 返回。
		_ = conn.Close()
		<-done
	})
	return server, conn.LocalAddr().(*net.UDPAddr)
}

// --- 用例 -------------------------------------------------------------------

func TestFrameCodecRoundTrip(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	frame := Frame{Type: FrameData, NodeID: id, Payload: []byte("hello relay")}
	encoded := frame.Encode()
	if len(encoded) != headerSize+len(frame.Payload) {
		t.Fatalf("编码长度 = %d，期望 %d", len(encoded), headerSize+len(frame.Payload))
	}
	if encoded[0] != FrameData {
		t.Fatalf("帧类型字节 = %d，期望 %d", encoded[0], FrameData)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("解析帧失败：%v", err)
	}
	if decoded.Type != frame.Type || decoded.NodeID != frame.NodeID || string(decoded.Payload) != string(frame.Payload) {
		t.Fatalf("往返结果不对：%+v", decoded)
	}
	if _, err := DecodeFrame([]byte{1, 2, 3}); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("短帧错误 = %v，期望 ErrShortFrame", err)
	}
}

func TestTokenCodecRoundTrip(t *testing.T) {
	payload := EncodeToken("secret-token")
	decoded, err := DecodeToken(payload)
	if err != nil {
		t.Fatalf("解析 token 失败：%v", err)
	}
	if decoded != "secret-token" {
		t.Fatalf("token = %q，期望 secret-token", decoded)
	}
	if _, err := DecodeToken([]byte{0x10, 0x00}); err == nil {
		t.Fatal("越界的 token 长度应该报错")
	}
}

func TestDataPayloadCarriesDestinationAndPacket(t *testing.T) {
	destination := uuid.MustParse("99999999-8888-7777-6666-555555555555")
	payload := EncodeData(destination, []byte{0x01, 0x02, 0x03})
	got, packet, err := DecodeData(payload)
	if err != nil {
		t.Fatalf("解析 DATA 载荷失败：%v", err)
	}
	if got != destination {
		t.Fatalf("目标 node id = %s，期望 %s", got, destination)
	}
	if len(packet) != 3 || packet[0] != 0x01 {
		t.Fatalf("原始报文被改动了：%v", packet)
	}
}

func TestRegisterRejectsInvalidToken(t *testing.T) {
	auth := newFakeAuthenticator()
	metrics := newFakeMetrics()
	server, address := startServer(t, ServerOptions{}, auth, metrics)
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	client := newTestClient(t, id)
	client.send(t, address, Frame{Type: FrameRegister, NodeID: id, Payload: EncodeToken("wrong")})
	frame := client.receive(t)
	if frame.Type != FrameError {
		t.Fatalf("帧类型 = %d，期望 ERROR(%d)", frame.Type, FrameError)
	}
	if len(frame.Payload) != 0 {
		t.Fatalf("ERROR 帧不应该泄露原因：%q", frame.Payload)
	}
	if server.Sessions() != 0 {
		t.Fatalf("鉴权失败不应该建立会话，会话数 = %d", server.Sessions())
	}
}

func TestRegisterSuccessReturnsPublicEndpoint(t *testing.T) {
	auth := newFakeAuthenticator()
	metrics := newFakeMetrics()
	server, address := startServer(t, ServerOptions{PublicEndpoint: "relay.example.com:51821"}, auth, metrics)
	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	client := newTestClient(t, id)
	client.send(t, address, Frame{Type: FrameRegister, NodeID: id, Payload: EncodeToken("valid-token")})
	frame := client.receive(t)
	if frame.Type != FrameRegistered {
		t.Fatalf("帧类型 = %d，期望 REGISTERED(%d)", frame.Type, FrameRegistered)
	}
	if string(frame.Payload) != "relay.example.com:51821" {
		t.Fatalf("REGISTERED 载荷 = %q", frame.Payload)
	}
	if frame.NodeID != id {
		t.Fatalf("REGISTERED 帧头的 node id = %s，期望 %s", frame.NodeID, id)
	}
	if server.Sessions() != 1 {
		t.Fatalf("会话数 = %d，期望 1", server.Sessions())
	}
	if metrics.sessions != 1 {
		t.Fatalf("会话指标 = %d，期望 1", metrics.sessions)
	}
}

// 两个客户端经中继互发 DATA 报文（真实回环，不需要公网）。
func TestRelayForwardsDataBetweenPeers(t *testing.T) {
	auth := newFakeAuthenticator()
	metrics := newFakeMetrics()
	_, address := startServer(t, ServerOptions{PublicEndpoint: "relay.example.com:51821"}, auth, metrics)

	nodeA := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	nodeB := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	clientA := newTestClient(t, nodeA)
	clientB := newTestClient(t, nodeB)
	clientA.send(t, address, Frame{Type: FrameRegister, NodeID: nodeA, Payload: EncodeToken("valid-token")})
	if frame := clientA.receive(t); frame.Type != FrameRegistered {
		t.Fatalf("A 注册失败：帧类型 %d", frame.Type)
	}
	clientB.send(t, address, Frame{Type: FrameRegister, NodeID: nodeB, Payload: EncodeToken("valid-token")})
	if frame := clientB.receive(t); frame.Type != FrameRegistered {
		t.Fatalf("B 注册失败：帧类型 %d", frame.Type)
	}

	packet := []byte{0xde, 0xad, 0xbe, 0xef}
	clientA.send(t, address, Frame{Type: FrameData, NodeID: nodeA, Payload: EncodeData(nodeB, packet)})
	frame := clientB.receive(t)
	if frame.Type != FrameData {
		t.Fatalf("帧类型 = %d，期望 DATA(%d)", frame.Type, FrameData)
	}
	if frame.NodeID != nodeA {
		t.Fatalf("帧头的源 node id = %s，期望 %s", frame.NodeID, nodeA)
	}
	destination, forwarded, err := DecodeData(frame.Payload)
	if err != nil {
		t.Fatalf("解析转发载荷失败：%v", err)
	}
	if destination != nodeB {
		t.Fatalf("目标 node id = %s，期望 %s", destination, nodeB)
	}
	if string(forwarded) != string(packet) {
		t.Fatalf("转发的报文被改动了：%v", forwarded)
	}
	if metrics.packetCount("out") != 1 {
		t.Fatalf("出向包数 = %d，期望 1", metrics.packetCount("out"))
	}
	if metrics.bytes != len(packet) {
		t.Fatalf("字节数 = %d，期望 %d", metrics.bytes, len(packet))
	}
}

func TestSessionExpiresAfterTTL(t *testing.T) {
	clock := newFakeClock()
	auth := newFakeAuthenticator()
	server, address := startServer(t, ServerOptions{Now: clock.Now, SessionTTL: 2 * time.Minute}, auth, newFakeMetrics())
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	client := newTestClient(t, id)
	client.send(t, address, Frame{Type: FrameRegister, NodeID: id, Payload: EncodeToken("valid-token")})
	client.receive(t)
	if server.Sessions() != 1 {
		t.Fatalf("会话数 = %d，期望 1", server.Sessions())
	}
	// 超过 TTL 没有流量 → 过期。
	clock.Advance(3 * time.Minute)
	server.expire()
	if server.Sessions() != 0 {
		t.Fatalf("过期后会话数 = %d，期望 0", server.Sessions())
	}
	// 过期后源地址不再被识别，DATA 一律丢弃。
	client.send(t, address, Frame{Type: FrameData, NodeID: id, Payload: EncodeData(uuid.New(), []byte{1})})
	client.expectNothing(t)
}

func TestRateLimitDropsExcessPackets(t *testing.T) {
	auth := newFakeAuthenticator()
	metrics := newFakeMetrics()
	server, address := startServer(t, ServerOptions{PacketLimit: 2}, auth, metrics)
	nodeA := uuid.MustParse("aaaaaaaa-1111-1111-1111-111111111111")
	nodeB := uuid.MustParse("bbbbbbbb-1111-1111-1111-111111111111")
	clientA := newTestClient(t, nodeA)
	clientB := newTestClient(t, nodeB)
	clientA.send(t, address, Frame{Type: FrameRegister, NodeID: nodeA, Payload: EncodeToken("valid-token")})
	clientA.receive(t)
	clientB.send(t, address, Frame{Type: FrameRegister, NodeID: nodeB, Payload: EncodeToken("valid-token")})
	clientB.receive(t)

	// 前 2 个包应当转发，第 3 个被限流丢弃。
	for index := 0; index < 2; index++ {
		clientA.send(t, address, Frame{Type: FrameData, NodeID: nodeA, Payload: EncodeData(nodeB, []byte{byte(index)})})
		frame := clientB.receive(t)
		if frame.Type != FrameData {
			t.Fatalf("第 %d 个包没有转发，帧类型 %d", index, frame.Type)
		}
	}
	clientA.send(t, address, Frame{Type: FrameData, NodeID: nodeA, Payload: EncodeData(nodeB, []byte{0xff})})
	clientB.expectNothing(t)
	if server.DroppedPackets() == 0 {
		t.Fatal("超限的包没有被计数")
	}
	if server.SessionDropped(nodeA) == 0 {
		t.Fatal("会话级丢包计数没有增加")
	}
	if metrics.packetCount("dropped") == 0 {
		t.Fatal("dropped 指标没有增加")
	}
}

func TestUnregisteredSourceIsDropped(t *testing.T) {
	auth := newFakeAuthenticator()
	server, address := startServer(t, ServerOptions{}, auth, newFakeMetrics())
	id := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	client := newTestClient(t, id)
	// 没登记就发 DATA → 丢弃。
	client.send(t, address, Frame{Type: FrameData, NodeID: id, Payload: EncodeData(uuid.New(), []byte{1, 2, 3})})
	client.expectNothing(t)
	if server.Sessions() != 0 {
		t.Fatalf("会话数 = %d，期望 0", server.Sessions())
	}
}

func TestOptionsFromEnvDefaults(t *testing.T) {
	t.Setenv(EnvEnabled, "")
	t.Setenv(EnvListen, "")
	t.Setenv(EnvPublicHost, "")
	options := OptionsFromEnv()
	if options.Enabled {
		t.Fatal("中继默认必须关闭")
	}
	if options.Listen != DefaultListen {
		t.Fatalf("默认监听地址 = %q，期望 %q", options.Listen, DefaultListen)
	}
	if options.PublicEndpoint() != "" {
		t.Fatalf("未配置公网地址时 PublicEndpoint = %q", options.PublicEndpoint())
	}

	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvListen, ":51821")
	t.Setenv(EnvPublicHost, "relay.example.com")
	options = OptionsFromEnv()
	if !options.Enabled {
		t.Fatal("NEILICO_RELAY_ENABLED=true 应该启用中继")
	}
	if options.PublicEndpoint() != "relay.example.com:51821" {
		t.Fatalf("PublicEndpoint = %q", options.PublicEndpoint())
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("校验失败：%v", err)
	}

	t.Setenv(EnvPublicHost, "")
	options = OptionsFromEnv()
	if err := options.Validate(); err == nil {
		t.Fatal("启用中继但没有公网地址时应该报错")
	}
}
