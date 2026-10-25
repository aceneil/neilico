package mesh

// 中继兜底（relay）：打洞失败时把 WireGuard 报文经服务器转发一遍。
//
// 为什么是「本地回环代理」而不是直接改 WireGuard 的 socket：
// 内核态 WireGuard 的 socket 拿不到（也改不了它的源地址），所以只能让 WireGuard
// 把对端 endpoint 指到本机的一个回环 UDP 端口上，由 agent 进程把报文读出来、
// 加上帧头发给中继；对端 agent 再把报文注入自己的回环端口，WireGuard 就收到了。
//
//	kernel wg0 ──peer endpoint=127.0.0.1:41000──▶ agent 本地代理
//	                                                │ UDP，带帧头
//	                                                ▼
////	                                          relay（控制面进程内）
//	                                                │ 按 dst node 转发
//	                                                ▼
//	                                          对端 agent ──▶ 注入 127.0.0.1:41000 ──▶ kernel wg0
//
// 直连可用时这套东西完全不启用（不增加任何开销）。

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"neilico/agent/internal/client"
)

const (
	// relayFrameRegister 是客户端登记会话的帧。
	relayFrameRegister byte = 1
	// relayFrameKeepalive 是客户端的保活帧。
	relayFrameKeepalive byte = 2
	// relayFrameData 是承载 WireGuard 报文的帧。
	relayFrameData byte = 3
	// relayFrameRegistered 是服务端登记成功的回执。
	relayFrameRegistered byte = 4
	// relayFrameError 是服务端的错误回执（不携带原因）。
	relayFrameError byte = 5

	// relayHeaderSize = 1 字节帧类型 + 16 字节 node id。
	relayHeaderSize = 17
	// relayNodeIDSize 是 node id（UUID）的字节数。
	relayNodeIDSize = 16

	// relayLocalPortBase 是本地代理端口的起始值（每个走中继的对端一个）。
	relayLocalPortBase = 41000
	// relayLocalPortAttempts 是本地代理连续绑定失败时的重试次数。
	relayLocalPortAttempts = 64
	// relayKeepaliveInterval 是保活周期（服务端会话 2 分钟过期）。
	relayKeepaliveInterval = 15 * time.Second
	// relayReconnectMin / relayReconnectMax 是重连的指数退避区间。
	relayReconnectMin = time.Second
	relayReconnectMax = 60 * time.Second
	// relayReadTimeout 是读超时（超时只用于探测连接还活着，不算断线）。
	relayReadTimeout = 60 * time.Second
	// relayFailoverDelay 是「直连确认不通」之后延迟多久才切到中继（给直连留机会）。
	relayFailoverDelay = 10 * time.Second
	// relayDirectPathStaleWindow 是握手超过多久之后认为直连路径已经死了。
	//
	// 必须大于 WireGuard 的重协商周期（120 秒）：握手时间戳在重协商之前不会刷新，
	// 用 10 秒这种短窗口会把好好的直连隧道误判成断线。
	relayDirectPathStaleWindow = DirectPathWindow
	// relayEvaluateInterval 是评估直连/中继的周期。
	relayEvaluateInterval = 5 * time.Second
	// relayPacketSize 是单个 UDP 数据包的上限。
	relayPacketSize = 64 * 1024
)

// DirectPathWindow 是判定「直连路径还活着」的握手时间窗。
//
// 必须大于 WireGuard 的重协商周期（120 秒）：握手时间戳在重协商之前不会刷新，
// 用 10 秒这种短窗口会把好好的直连隧道误判成断线。
const DirectPathWindow = 180 * time.Second

// --- 帧编解码 ---------------------------------------------------------------

// encodeRelayFrame 编码一个中继帧：1 字节类型 + 16 字节 node id + 载荷。
func encodeRelayFrame(frameType byte, nodeID [relayNodeIDSize]byte, payload []byte) []byte {
	buffer := make([]byte, relayHeaderSize+len(payload))
	buffer[0] = frameType
	copy(buffer[1:relayHeaderSize], nodeID[:])
	copy(buffer[relayHeaderSize:], payload)
	return buffer
}

// decodeRelayFrame 解析一个中继帧。
func decodeRelayFrame(data []byte) (byte, [relayNodeIDSize]byte, []byte, error) {
	var nodeID [relayNodeIDSize]byte
	if len(data) < relayHeaderSize {
		return 0, nodeID, nil, fmt.Errorf("中继帧长度不足：%d", len(data))
	}
	copy(nodeID[:], data[1:relayHeaderSize])
	payload := make([]byte, len(data)-relayHeaderSize)
	copy(payload, data[relayHeaderSize:])
	return data[0], nodeID, payload, nil
}

// encodeRelayToken 编码 REGISTER 帧的 token 载荷：token_len(2 字节小端) + token。
func encodeRelayToken(token string) []byte {
	buffer := make([]byte, 2+len(token))
	binary.LittleEndian.PutUint16(buffer, uint16(len(token)))
	copy(buffer[2:], token)
	return buffer
}

// decodeRelayToken 解析 REGISTER 帧的 token 载荷。
func decodeRelayToken(payload []byte) (string, error) {
	if len(payload) < 2 {
		return "", errors.New("中继 token 载荷长度不足")
	}
	length := int(binary.LittleEndian.Uint16(payload[:2]))
	if length > len(payload)-2 {
		return "", errors.New("中继 token 长度越界")
	}
	return string(payload[2 : 2+length]), nil
}

// encodeRelayData 编码 DATA 帧载荷：目标 node id（16 字节）+ 原始 WireGuard 报文。
func encodeRelayData(destination [relayNodeIDSize]byte, packet []byte) []byte {
	payload := make([]byte, relayNodeIDSize+len(packet))
	copy(payload[:relayNodeIDSize], destination[:])
	copy(payload[relayNodeIDSize:], packet)
	return payload
}

// decodeRelayData 解析 DATA 帧载荷。
func decodeRelayData(payload []byte) ([relayNodeIDSize]byte, []byte, error) {
	var destination [relayNodeIDSize]byte
	if len(payload) < relayNodeIDSize {
		return destination, nil, errors.New("中继 DATA 载荷长度不足")
	}
	copy(destination[:], payload[:relayNodeIDSize])
	packet := make([]byte, len(payload)-relayNodeIDSize)
	copy(packet, payload[relayNodeIDSize:])
	return destination, packet, nil
}

// parseRelayNodeID 把 UUID 字符串（控制面下发的 relay_node_id）转成 16 字节。
//
// 这里自己解析而不是引第三方库：格式是固定的 8-4-4-4-12 十六进制串，
// 少一个依赖就少一份供应链负担。
func parseRelayNodeID(value string) ([relayNodeIDSize]byte, error) {
	var result [relayNodeIDSize]byte
	cleaned := strings.TrimSpace(value)
	cleaned = strings.TrimPrefix(cleaned, "{")
	cleaned = strings.TrimSuffix(cleaned, "}")
	if len(cleaned) != 36 {
		return result, fmt.Errorf("node id %q 不是合法的 UUID", value)
	}
	groups := []int{8, 4, 4, 4, 12}
	offset := 0
	position := 0
	for _, size := range groups {
		if position+size > len(cleaned) || cleaned[position] == '-' {
			return result, fmt.Errorf("node id %q 不是合法的 UUID", value)
		}
		for index := 0; index < size; index++ {
			digit, err := strconv.ParseUint(string(cleaned[position+index]), 16, 8)
			if err != nil {
				return result, fmt.Errorf("node id %q 不是合法的 UUID", value)
			}
			result[offset] = byte(digit)
			offset++
		}
		position += size
		if size != groups[len(groups)-1] {
			if position >= len(cleaned) || cleaned[position] != '-' {
				return result, fmt.Errorf("node id %q 不是合法的 UUID", value)
			}
			position++
		}
	}
	return result, nil
}

// --- 本地代理 ---------------------------------------------------------------

// LocalProxy 是「走中继的某个对端」在回环上的 UDP 端口。
//
// WireGuard 看到的对端 endpoint 就是这个地址（127.0.0.1:41000 起）；报文从内核
// 出来之后由这里读走，塞进 DATA 帧发给中继；中继回来的报文也从这个 socket 发出去
// （**必须**是同一个 socket：内核 WireGuard 会校验数据包的源地址等于对端 endpoint）。
type LocalProxy struct {
	conn    *net.UDPConn
	address string
	local   *net.UDPAddr
}

// newLocalProxy 在 127.0.0.1:port 上监听（port=0 表示由系统分配）。
func newLocalProxy(port int) (*LocalProxy, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		return nil, fmt.Errorf("监听本地中继端口 %d 失败: %w", port, err)
	}
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		conn.Close()
		return nil, errors.New("本地中继端口不是 UDP 地址")
	}
	return &LocalProxy{conn: conn, address: local.String(), local: local}, nil
}

// Address 返回本地代理的地址（127.0.0.1:port），也就是 WireGuard 要用的 endpoint。
func (p *LocalProxy) Address() string { return p.address }

// Serve 读取内核送来的报文并交给 onPacket（ctx 结束时返回）。
func (p *LocalProxy) Serve(ctx context.Context, onPacket func([]byte)) {
	buffer := make([]byte, relayPacketSize)
	for {
		if ctx.Err() != nil {
			return
		}
		length, _, err := p.conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		packet := make([]byte, length)
		copy(packet, buffer[:length])
		if onPacket != nil {
			onPacket(packet)
		}
	}
}

// Deliver 把中继回来的报文注入本地端口（WireGuard 会收到它）。
func (p *LocalProxy) Deliver(packet []byte) error {
	_, err := p.conn.WriteToUDP(packet, p.local)
	return err
}

// Close 关闭本地代理。
func (p *LocalProxy) Close() error { return p.conn.Close() }

// --- 中继客户端 -------------------------------------------------------------

// RelayClientOptions 是中继客户端的可注入依赖。
type RelayClientOptions struct {
	// NodeID 是本机的 node uuid（REGISTER 时填进帧头）。
	NodeID string
	// Token 是本机的 agent token（REGISTER 时鉴权用）。
	Token string
	// Endpoint 是中继地址（host:port）。
	Endpoint string
	// Logger 是日志出口。
	Logger *slog.Logger
	// Now 是时钟（单测可注入）。
	Now func() time.Time
	// KeepaliveInterval 是保活周期。
	KeepaliveInterval time.Duration
	// ReconnectMin / ReconnectMax 是重连退避区间。
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	// DialUDP 建立到中继的连接（单测可注入）。
	DialUDP func(ctx context.Context, address string) (*net.UDPConn, error)
	// OnData 收到 DATA 帧时回调（源 node id + 原始 WireGuard 报文）。
	OnData func(source [relayNodeIDSize]byte, packet []byte)
	// OnState 状态变化回调（registered / error / reconnecting）。
	OnState func(state string, err error)
}

// RelayClient 是到中继的一个 UDP 连接：负责 REGISTER / KEEPALIVE / DATA。
type RelayClient struct {
	options    RelayClientOptions
	nodeID     [relayNodeIDSize]byte
	mutex      sync.Mutex
	conn       *net.UDPConn
	registered bool
	backoff    time.Duration
}

// NewRelayClient 构造中继客户端。
func NewRelayClient(options RelayClientOptions) (*RelayClient, error) {
	nodeID, err := parseRelayNodeID(options.NodeID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.Endpoint) == "" {
		return nil, errors.New("中继地址为空")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.KeepaliveInterval <= 0 {
		options.KeepaliveInterval = relayKeepaliveInterval
	}
	if options.ReconnectMin <= 0 {
		options.ReconnectMin = relayReconnectMin
	}
	if options.ReconnectMax <= 0 {
		options.ReconnectMax = relayReconnectMax
	}
	if options.DialUDP == nil {
		options.DialUDP = dialRelayUDP
	}
	return &RelayClient{options: options, nodeID: nodeID}, nil
}

// dialRelayUDP 建立到中继的 UDP 连接。
func dialRelayUDP(ctx context.Context, address string) (*net.UDPConn, error) {
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("解析中继地址 %s 失败: %w", address, err)
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return nil, fmt.Errorf("连接中继 %s 失败: %w", address, err)
	}
	return conn, nil
}

// Run 维护与中继的连接：断线后指数退避重连（上限 60s）。ctx 结束时返回。
func (c *RelayClient) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := c.options.DialUDP(ctx, c.options.Endpoint)
		if err != nil {
			c.state("dial_failed", err)
			if !c.wait(ctx, c.nextBackoff()) {
				return
			}
			continue
		}
		c.setConn(conn)
		c.register(conn)
		go c.keepalive(ctx, conn)
		readErr := c.read(ctx, conn)
		c.clearConn(conn)
		_ = conn.Close()
		if ctx.Err() != nil {
			return
		}
		c.state("reconnecting", readErr)
		if !c.wait(ctx, c.nextBackoff()) {
			return
		}
	}
}

// register 发 REGISTER 帧。
func (c *RelayClient) register(conn *net.UDPConn) {
	frame := encodeRelayFrame(relayFrameRegister, c.nodeID, encodeRelayToken(c.options.Token))
	if _, err := conn.Write(frame); err != nil {
		c.state("register_failed", err)
	}
}

// keepalive 周期性发 KEEPALIVE 帧（服务端会话 2 分钟过期）。
func (c *RelayClient) keepalive(ctx context.Context, conn *net.UDPConn) {
	ticker := time.NewTicker(c.options.KeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame := encodeRelayFrame(relayFrameKeepalive, c.nodeID, nil)
			if _, err := conn.Write(frame); err != nil {
				return
			}
		}
	}
}

// read 读取中继发来的帧。
func (c *RelayClient) read(ctx context.Context, conn *net.UDPConn) error {
	buffer := make([]byte, relayPacketSize)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = conn.SetReadDeadline(c.options.Now().Add(relayReadTimeout))
		length, err := conn.Read(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// 只是读超时：连接还在，继续等。
				continue
			}
			return err
		}
		c.handle(buffer[:length])
	}
}

// handle 处理一个收到的帧。
func (c *RelayClient) handle(data []byte) {
	frameType, source, payload, err := decodeRelayFrame(data)
	if err != nil {
		return
	}
	switch frameType {
	case relayFrameRegistered:
		c.mutex.Lock()
		c.registered = true
		c.mutex.Unlock()
		c.state("registered", nil)
	case relayFrameError:
		// 服务端不泄露失败原因，这里也只记一个不含细节的状态。
		c.state("rejected", errors.New("中继拒绝了本次登记"))
	case relayFrameData:
		_, packet, err := decodeRelayData(payload)
		if err != nil {
			return
		}
		if c.options.OnData != nil {
			c.options.OnData(source, packet)
		}
	}
}

// SendData 把一条 WireGuard 报文发给中继（目标是对端的 node id）。
func (c *RelayClient) SendData(destination [relayNodeIDSize]byte, packet []byte) error {
	c.mutex.Lock()
	conn := c.conn
	registered := c.registered
	c.mutex.Unlock()
	if conn == nil {
		return errors.New("中继尚未连接")
	}
	if !registered {
		return errors.New("中继尚未登记")
	}
	frame := encodeRelayFrame(relayFrameData, c.nodeID, encodeRelayData(destination, packet))
	_, err := conn.Write(frame)
	return err
}

// Registered 返回当前是否已经登记成功。
func (c *RelayClient) Registered() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.registered
}

func (c *RelayClient) setConn(conn *net.UDPConn) {
	c.mutex.Lock()
	c.conn = conn
	c.registered = false
	c.mutex.Unlock()
}

func (c *RelayClient) clearConn(conn *net.UDPConn) {
	c.mutex.Lock()
	if c.conn == conn {
		c.conn = nil
		c.registered = false
	}
	c.mutex.Unlock()
}

// nextBackoff 返回下一次重连的等待时间（指数退避，上限 ReconnectMax）。
func (c *RelayClient) nextBackoff() time.Duration {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.backoff <= 0 {
		c.backoff = c.options.ReconnectMin
	} else {
		c.backoff *= 2
	}
	if c.backoff > c.options.ReconnectMax {
		c.backoff = c.options.ReconnectMax
	}
	return c.backoff
}

// wait 等待一段时间（ctx 结束时返回 false）。
func (c *RelayClient) wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *RelayClient) state(state string, err error) {
	if c.options.OnState != nil {
		c.options.OnState(state, err)
		return
	}
	if c.options.Logger == nil {
		return
	}
	if err != nil {
		c.options.Logger.Warn("中继状态变化", "state", state, "error", err.Error())
		return
	}
	c.options.Logger.Info("中继状态变化", "state", state)
}

// --- 中继管理器 -------------------------------------------------------------

// RelayPeer 是「需要走中继」的一个对端。
type RelayPeer struct {
	// NodeID 是对端的 node uuid（控制面下发的 relay_node_id）：中继按它转发报文。
	NodeID string
	// PublicKey 是对端的 WireGuard 公钥（设置 endpoint 用）。
	PublicKey string
	// Endpoint 是中继地址（host:port）。
	Endpoint string
}

// RelayPeersFromPeers 从控制面下发的对端列表里挑出「需要走中继」的对端。
//
// 没有 relay_endpoint 的对端（直连可用、老版本控制面）直接跳过——它们一个字节
// 都不会被改动，行为与中继功能上线前完全一致。
func RelayPeersFromPeers(peers []client.Peer) []RelayPeer {
	result := make([]RelayPeer, 0, len(peers))
	for _, peer := range peers {
		endpoint := strings.TrimSpace(peer.RelayEndpoint)
		nodeID := strings.TrimSpace(peer.RelayNodeID)
		if endpoint == "" || nodeID == "" {
			continue
		}
		result = append(result, RelayPeer{NodeID: nodeID, PublicKey: peer.PublicKey, Endpoint: endpoint})
	}
	return result
}

// RelayOptions 是中继管理器的可注入依赖（单测里全部可以替换成假实现）。
type RelayOptions struct {
	// NodeID 是本机的 node uuid。
	NodeID string
	// Token 是本机的 agent token（中继登记时鉴权用）。
	Token string
	// Logger 是日志出口。
	Logger *slog.Logger
	// Now 是时钟（单测可注入）。
	Now func() time.Time
	// SetEndpoint 设置 WireGuard 对端的 endpoint（默认走 wgctrl）。
	SetEndpoint func(ctx context.Context, publicKey, endpoint string) error
	// PeerState 读取对端当前的 endpoint 与最近一次握手时间。
	PeerState func(ctx context.Context, publicKey string) (string, time.Time, error)
	// EvaluateInterval 是评估直连/中继的周期。
	EvaluateInterval time.Duration
	// FailoverDelay 是「直连确认不通」之后等待多久才切到中继。
	FailoverDelay time.Duration
	// StaleWindow 是握手超过多久之后认为直连路径已经死了。
	//
	// 注意：WireGuard 的重协商周期是 120 秒，握手时间戳在这期间不会刷新，
	// 所以不能用 10 秒这种短窗口判断「直连不通」——那会把好好的直连隧道误判成
	// 断线并切到中继。
	StaleWindow time.Duration
	// LocalPortBase 是本地代理端口的起始值。
	LocalPortBase int
	// DialUDP 建立到中继的连接（单测可注入）。
	DialUDP func(ctx context.Context, address string) (*net.UDPConn, error)
}

// relayBinding 是一个「走中继的对端」的本地状态。
type relayBinding struct {
	peer   RelayPeer
	nodeID [relayNodeIDSize]byte
	proxy  *LocalProxy
	client *RelayClient
}

// RelayManager 管理中继客户端、本地代理，以及直连失败时的接管。
type RelayManager struct {
	options  RelayOptions
	mutex    sync.Mutex
	clients  map[string]*RelayClient
	bindings map[string]*relayBinding
	// failures 记录每个对端「直连不通」的起始时间（用于延迟接管）。
	failures map[string]time.Time
	// portCursor 是本地代理端口的分配游标。
	portCursor int
	ctx        context.Context
	started    bool
}

// NewRelayManager 构造中继管理器。
func NewRelayManager(options RelayOptions) *RelayManager {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.EvaluateInterval <= 0 {
		options.EvaluateInterval = relayEvaluateInterval
	}
	if options.FailoverDelay <= 0 {
		options.FailoverDelay = relayFailoverDelay
	}
	if options.StaleWindow <= 0 {
		options.StaleWindow = relayDirectPathStaleWindow
	}
	if options.LocalPortBase <= 0 {
		options.LocalPortBase = relayLocalPortBase
	}
	return &RelayManager{
		options:  options,
		clients:  map[string]*RelayClient{},
		bindings: map[string]*relayBinding{},
		failures: map[string]time.Time{},
	}
}

// Start 启动中继管理器（评估循环）。可以重复调用，只有第一次生效。
func (m *RelayManager) Start(ctx context.Context) {
	m.mutex.Lock()
	if m.started {
		m.mutex.Unlock()
		return
	}
	m.started = true
	if m.ctx == nil {
		m.ctx = ctx
	}
	m.mutex.Unlock()
	go m.evaluateLoop(ctx)
}

// Apply 应用（或撤销）对端的走中继配置。
//
// 每个「走中继的 peer」一个本地回环端口：WireGuard 侧只看到 127.0.0.1:port。
// 直连可用时（没有 relay_endpoint）这里什么都不做。
func (m *RelayManager) Apply(ctx context.Context, peers []RelayPeer) error {
	m.mutex.Lock()
	if m.ctx == nil {
		m.ctx = ctx
	}
	m.mutex.Unlock()

	var failures []error
	keep := map[string]bool{}
	for _, peer := range peers {
		peer.NodeID = strings.TrimSpace(peer.NodeID)
		peer.PublicKey = strings.TrimSpace(peer.PublicKey)
		peer.Endpoint = strings.TrimSpace(peer.Endpoint)
		if peer.NodeID == "" || peer.PublicKey == "" || peer.Endpoint == "" {
			continue
		}
		keep[peer.NodeID] = true
		if err := m.ensureBinding(ctx, peer); err != nil {
			failures = append(failures, err)
		}
	}
	// 不再走中继的对端：关掉本地代理（回收端口）。
	m.mutex.Lock()
	for nodeID, binding := range m.bindings {
		if keep[nodeID] {
			continue
		}
		if binding.proxy != nil {
			_ = binding.proxy.Close()
		}
		delete(m.bindings, nodeID)
		delete(m.failures, nodeID)
	}
	m.mutex.Unlock()
	return errors.Join(failures...)
}

// ensureBinding 保证这个对端有本地代理 + 中继客户端。
func (m *RelayManager) ensureBinding(ctx context.Context, peer RelayPeer) error {
	nodeID, err := parseRelayNodeID(peer.NodeID)
	if err != nil {
		return fmt.Errorf("对端 %s 的 node id 非法: %w", peer.NodeID, err)
	}
	client, err := m.ensureClient(ctx, peer.Endpoint)
	if err != nil {
		return err
	}
	m.mutex.Lock()
	binding, exists := m.bindings[peer.NodeID]
	if exists {
		binding.peer = peer
		binding.client = client
		binding.nodeID = nodeID
		m.mutex.Unlock()
		return nil
	}
	m.mutex.Unlock()

	proxy, err := m.allocateProxy()
	if err != nil {
		return err
	}
	binding = &relayBinding{peer: peer, nodeID: nodeID, proxy: proxy, client: client}
	m.mutex.Lock()
	m.bindings[peer.NodeID] = binding
	m.mutex.Unlock()

	runCtx := m.context()
	go proxy.Serve(runCtx, func(packet []byte) {
		if err := client.SendData(nodeID, packet); err != nil && m.options.Logger != nil {
			m.options.Logger.Debug("中继发送失败", "peer", peer.NodeID, "error", err.Error())
		}
	})
	return nil
}

// ensureClient 保证到该中继地址的客户端存在（并已在后台跑重连循环）。
func (m *RelayManager) ensureClient(ctx context.Context, endpoint string) (*RelayClient, error) {
	m.mutex.Lock()
	client, ok := m.clients[endpoint]
	m.mutex.Unlock()
	if ok {
		return client, nil
	}
	client, err := NewRelayClient(RelayClientOptions{
		NodeID:   m.options.NodeID,
		Token:    m.options.Token,
		Endpoint: endpoint,
		Logger:   m.options.Logger,
		Now:      m.options.Now,
		DialUDP:  m.options.DialUDP,
		OnData:   m.onData,
		OnState:  m.onState,
	})
	if err != nil {
		return nil, err
	}
	m.mutex.Lock()
	if existing, exists := m.clients[endpoint]; exists {
		m.mutex.Unlock()
		return existing, nil
	}
	m.clients[endpoint] = client
	m.mutex.Unlock()
	go client.Run(m.context())
	return client, nil
}

// allocateProxy 分配一个本地回环端口（从 LocalPortBase 起）。
func (m *RelayManager) allocateProxy() (*LocalProxy, error) {
	m.mutex.Lock()
	base := m.options.LocalPortBase
	m.mutex.Unlock()
	for attempt := 0; attempt < relayLocalPortAttempts; attempt++ {
		m.mutex.Lock()
		port := base + m.portCursor
		m.portCursor++
		m.mutex.Unlock()
		proxy, err := newLocalProxy(port)
		if err == nil {
			return proxy, nil
		}
	}
	// 端口段被占满 → 交给系统分配一个。
	return newLocalProxy(0)
}

// onData 处理中继转发过来的报文：按源 node id 找到对应的本地代理并注入。
func (m *RelayManager) onData(source [relayNodeIDSize]byte, packet []byte) {
	m.mutex.Lock()
	var binding *relayBinding
	for _, item := range m.bindings {
		if item.nodeID == source {
			binding = item
			break
		}
	}
	m.mutex.Unlock()
	if binding == nil {
		return
	}
	if err := binding.proxy.Deliver(packet); err != nil && m.options.Logger != nil {
		m.options.Logger.Debug("中继回包注入失败", "peer", binding.peer.NodeID, "error", err.Error())
	}
}

func (m *RelayManager) onState(state string, err error) {
	if m.options.Logger == nil {
		return
	}
	if err != nil {
		m.options.Logger.Warn("中继连接状态变化", "state", state, "error", err.Error())
		return
	}
	m.options.Logger.Info("中继连接状态变化", "state", state)
}

// evaluateLoop 周期评估：直连确认不通时把对端切到中继（兜底）。
//
// 规则：
//   - 直连有近期握手 → **不动** endpoint（打洞器/控制面设的直连 endpoint 保持不变，
//     中继代理继续留着不断开，只是不用）；
//   - 直连确认不通（从没握手过，或握手已经超过 StaleWindow）→ 连续观察
//     FailoverDelay 之后把 endpoint 切到本地中继端口。
func (m *RelayManager) evaluateLoop(ctx context.Context) {
	ticker := time.NewTicker(m.options.EvaluateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.evaluate(ctx)
		}
	}
}

// evaluate 执行一轮评估。
func (m *RelayManager) evaluate(ctx context.Context) {
	m.mutex.Lock()
	bindings := make([]*relayBinding, 0, len(m.bindings))
	for _, binding := range m.bindings {
		bindings = append(bindings, binding)
	}
	m.mutex.Unlock()

	for _, binding := range bindings {
		m.evaluateBinding(ctx, binding)
	}
}

// evaluateBinding 评估一个对端是否需要切到中继。
func (m *RelayManager) evaluateBinding(ctx context.Context, binding *relayBinding) {
	if m.options.PeerState == nil || m.options.SetEndpoint == nil {
		return
	}
	endpoint, handshake, err := m.options.PeerState(ctx, binding.peer.PublicKey)
	if err != nil {
		return
	}
	now := m.options.Now()
	if m.directAlive(handshake, now) {
		m.mutex.Lock()
		delete(m.failures, binding.peer.PublicKey)
		m.mutex.Unlock()
		return
	}
	// 直连不通：记录起始时间，连续观察够久才接管（给直连留出机会）。
	m.mutex.Lock()
	first, seen := m.failures[binding.peer.PublicKey]
	if !seen {
		first = now
		m.failures[binding.peer.PublicKey] = first
	}
	m.mutex.Unlock()
	if now.Sub(first) < m.options.FailoverDelay {
		return
	}
	relayEndpoint := binding.proxy.Address()
	if endpoint == relayEndpoint {
		return
	}
	if err := m.options.SetEndpoint(ctx, binding.peer.PublicKey, relayEndpoint); err != nil {
		if m.options.Logger != nil {
			m.options.Logger.Warn("切换到中继失败", "peer", binding.peer.NodeID, "error", err.Error())
		}
		return
	}
	m.mutex.Lock()
	delete(m.failures, binding.peer.PublicKey)
	m.mutex.Unlock()
	if m.options.Logger != nil {
		m.options.Logger.Info("直连不通，已切换到中继兜底",
			"peer", binding.peer.NodeID, "endpoint", relayEndpoint)
	}
}

// directAlive 判断直连路径是否还活着。
//
// 判据是「最近一次握手有多旧」：WireGuard 的重协商周期是 120 秒，握手时间戳在这
// 期间不会刷新，所以 180 秒以内的握手都还算活着（没握手过 = 从来没通）。
func (m *RelayManager) directAlive(handshake time.Time, now time.Time) bool {
	if handshake.IsZero() {
		return false
	}
	age := now.Sub(handshake)
	if age < 0 {
		age = 0
	}
	return age <= m.options.StaleWindow
}

// context 返回长生命周期的上下文（代理与客户端的后台循环用它）。
func (m *RelayManager) context() context.Context {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// Close 关闭所有本地代理。
func (m *RelayManager) Close() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for _, binding := range m.bindings {
		if binding.proxy != nil {
			_ = binding.proxy.Close()
		}
	}
	m.bindings = map[string]*relayBinding{}
}
