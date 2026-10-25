package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// defaultSessionTTL 是会话的存活时间：2 分钟内没有任何流量就过期。
	defaultSessionTTL = 2 * time.Minute
	// defaultPacketLimit 是单个会话每秒允许的包数上限。
	defaultPacketLimit = 2000
	// defaultByteLimit 是单个会话每秒允许的字节数上限（20 MB/s）。
	defaultByteLimit = 20 * 1024 * 1024
	// expireInterval 是清理过期会话的周期。
	expireInterval = 30 * time.Second
	// maxUDPRead 是单次 UDP 读取的上限。
	maxUDPRead = 64 * 1024
)

// Authenticator 校验中继会话的归属：node id + agent token 必须匹配控制面里
// 已登记的节点（复用节点心跳那套鉴权路径）。
//
// 用接口而不是直接依赖 service 包，是为了避免包循环依赖，也方便单测注入假实现。
type Authenticator interface {
	AuthenticateRelay(ctx context.Context, nodeID uuid.UUID, token string) error
}

// Metrics 是中继要暴露的 Prometheus 指标（由 control-plane 的 metrics 实现）。
type Metrics interface {
	ObserveRelayPacket(direction string, count int)
	ObserveRelayBytes(count int)
	SetRelaySessions(count int)
}

// ServerOptions 是中继服务端的行为参数。
type ServerOptions struct {
	// Listen 是 UDP 监听地址（默认 :51821）。
	Listen string
	// PublicEndpoint 是回给客户端的公网地址（public_host:port）。
	PublicEndpoint string
	// SessionTTL 是会话过期时间（默认 2 分钟）。
	SessionTTL time.Duration
	// PacketLimit 是每会话每秒的包数上限（默认 2000）。
	PacketLimit int
	// ByteLimit 是每会话每秒的字节数上限（默认 20 MB/s）。
	ByteLimit int
	// Logger 是日志出口。
	Logger *slog.Logger
	// Now 是时钟（单测可注入假时钟）。
	Now func() time.Time
}

// session 是一个已登记的中继会话。
type session struct {
	nodeID      uuid.UUID
	addr        *net.UDPAddr
	lastSeen    time.Time
	windowStart time.Time
	windowCount int
	windowBytes int
	dropped     uint64
}

// Server 是中继服务端：登记会话、按目标 node id 转发 DATA 帧。
type Server struct {
	options       ServerOptions
	authenticator Authenticator
	metrics       Metrics
	logger        *slog.Logger
	now           func() time.Time

	mutex    sync.Mutex
	sessions map[uuid.UUID]*session
	byAddr   map[string]uuid.UUID
	conn     *net.UDPConn
	dropped  uint64
}

// New 构造中继服务端。
func New(options ServerOptions, authenticator Authenticator, metrics Metrics) *Server {
	if options.Listen == "" {
		options.Listen = DefaultListen
	}
	if options.SessionTTL <= 0 {
		options.SessionTTL = defaultSessionTTL
	}
	if options.PacketLimit <= 0 {
		options.PacketLimit = defaultPacketLimit
	}
	if options.ByteLimit <= 0 {
		options.ByteLimit = defaultByteLimit
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Server{
		options:       options,
		authenticator: authenticator,
		metrics:       metrics,
		logger:        options.Logger,
		now:           options.Now,
		sessions:      map[uuid.UUID]*session{},
		byAddr:        map[string]uuid.UUID{},
	}
}

// Listen 在配置的地址上监听 UDP。
func (s *Server) Listen() (*net.UDPConn, error) {
	address, err := net.ResolveUDPAddr("udp", s.options.Listen)
	if err != nil {
		return nil, fmt.Errorf("relay: 解析监听地址 %s 失败: %w", s.options.Listen, err)
	}
	conn, err := net.ListenUDP("udp", address)
	if err != nil {
		return nil, fmt.Errorf("relay: 监听 %s 失败: %w", s.options.Listen, err)
	}
	s.mutex.Lock()
	s.conn = conn
	s.mutex.Unlock()
	return conn, nil
}

// Serve 启动中继服务（阻塞）。ctx 结束时返回。
func (s *Server) Serve(ctx context.Context) error {
	conn, err := s.Listen()
	if err != nil {
		return err
	}
	defer conn.Close()
	return s.ServeConn(ctx, conn)
}

// ServeConn 在已建立的 UDP 连接上运行中继服务（单测可直接传入本地 socket）。
func (s *Server) ServeConn(ctx context.Context, conn *net.UDPConn) error {
	s.mutex.Lock()
	s.conn = conn
	s.mutex.Unlock()
	go s.expireLoop(ctx)
	buffer := make([]byte, maxUDPRead)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		length, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return fmt.Errorf("relay: 读取 UDP 数据包失败: %w", err)
		}
		payload := make([]byte, length)
		copy(payload, buffer[:length])
		s.handle(ctx, payload, addr)
	}
}

// handle 处理一个收到的帧。
func (s *Server) handle(ctx context.Context, data []byte, addr *net.UDPAddr) {
	frame, err := DecodeFrame(data)
	if err != nil {
		s.countPacket("dropped", 1)
		return
	}
	switch frame.Type {
	case FrameRegister:
		s.handleRegister(ctx, frame, addr)
	case FrameKeepalive:
		s.handleKeepalive(frame, addr)
	case FrameData:
		s.handleData(frame, addr)
	default:
		// REGISTERED / ERROR 是服务端 → 客户端的帧，客户端发过来的一律丢弃。
		s.countPacket("dropped", 1)
	}
}

// handleRegister 登记会话：node id + token 必须通过鉴权，否则回 ERROR（不泄露原因）。
func (s *Server) handleRegister(ctx context.Context, frame Frame, addr *net.UDPAddr) {
	token, err := DecodeToken(frame.Payload)
	if err != nil {
		s.replyError(frame.NodeID, addr)
		return
	}
	if s.authenticator == nil {
		s.replyError(frame.NodeID, addr)
		return
	}
	if err := s.authenticator.AuthenticateRelay(ctx, frame.NodeID, token); err != nil {
		if s.logger != nil {
			s.logger.Warn("relay registration rejected", "node_id", frame.NodeID.String())
		}
		s.replyError(frame.NodeID, addr)
		return
	}
	s.registerSession(frame.NodeID, addr)
	payload := []byte(s.options.PublicEndpoint)
	s.write(frame.NodeID, FrameRegistered, payload, addr)
	s.countPacket("in", 1)
}

// handleKeepalive 刷新会话的存活时间。
func (s *Server) handleKeepalive(frame Frame, addr *net.UDPAddr) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	current, ok := s.byAddr[addrKey(addr)]
	if !ok {
		s.dropped++
		return
	}
	if entry, exists := s.sessions[current]; exists {
		entry.lastSeen = s.now()
	}
	_ = frame
}

// handleData 按目标 node id 转发 WireGuard 报文。
func (s *Server) handleData(frame Frame, addr *net.UDPAddr) {
	s.mutex.Lock()
	senderID, known := s.byAddr[addrKey(addr)]
	sender, hasSender := s.sessions[senderID]
	if hasSender {
		sender.lastSeen = s.now()
	}
	s.mutex.Unlock()
	if !known || !hasSender {
		// 没登记过的来源一律丢弃（防止有人拿中继当开放转发器）。
		s.countPacket("dropped", 1)
		return
	}
	if !s.allow(sender, len(frame.Payload)) {
		s.countPacket("dropped", 1)
		return
	}
	destination, packet, err := DecodeData(frame.Payload)
	if err != nil {
		s.countPacket("dropped", 1)
		return
	}
	if destination == senderID {
		// 自己发给自己没有意义（也避免回环）。
		s.countPacket("dropped", 1)
		return
	}
	s.mutex.Lock()
	target, exists := s.sessions[destination]
	s.mutex.Unlock()
	if !exists {
		// 目标还没登记（或者已经过期）→ 丢包。
		s.countPacket("dropped", 1)
		return
	}
	// 帧头回填**源** node id：接收方据此知道该把报文交给哪个本地代理。
	s.write(senderID, FrameData, EncodeData(destination, packet), target.addr)
	s.countPacket("out", 1)
	s.countBytes(len(packet))
}

// registerSession 登记/刷新一个会话。
func (s *Server) registerSession(nodeID uuid.UUID, addr *net.UDPAddr) {
	key := addrKey(addr)
	s.mutex.Lock()
	existing, ok := s.sessions[nodeID]
	if ok {
		if previous := addrKey(existing.addr); previous != key {
			delete(s.byAddr, previous)
		}
		existing.addr = addr
		existing.lastSeen = s.now()
		existing.windowStart = time.Time{}
		existing.windowCount = 0
		existing.windowBytes = 0
	} else {
		s.sessions[nodeID] = &session{nodeID: nodeID, addr: addr, lastSeen: s.now()}
	}
	s.byAddr[key] = nodeID
	count := len(s.sessions)
	s.mutex.Unlock()
	if s.metrics != nil {
		s.metrics.SetRelaySessions(count)
	}
}

// allow 判断该会话是否超过限流阈值；超限丢包并计数。
func (s *Server) allow(sender *session, size int) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := s.now()
	if sender.windowStart.IsZero() || now.Sub(sender.windowStart) >= time.Second {
		sender.windowStart = now
		sender.windowCount = 0
		sender.windowBytes = 0
	}
	sender.windowCount++
	sender.windowBytes += size
	if sender.windowCount > s.options.PacketLimit || sender.windowBytes > s.options.ByteLimit {
		sender.dropped++
		s.dropped++
		return false
	}
	return true
}

// expireLoop 定期清理过期会话。
func (s *Server) expireLoop(ctx context.Context) {
	ticker := time.NewTicker(expireInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expire()
		}
	}
}

// expire 清理超过 TTL 没有流量的会话。
func (s *Server) expire() {
	s.mutex.Lock()
	cutoff := s.now().Add(-s.options.SessionTTL)
	for id, entry := range s.sessions {
		if entry.lastSeen.Before(cutoff) {
			delete(s.sessions, id)
			delete(s.byAddr, addrKey(entry.addr))
		}
	}
	count := len(s.sessions)
	s.mutex.Unlock()
	if s.metrics != nil {
		s.metrics.SetRelaySessions(count)
	}
}

// write 回一个帧给客户端。
func (s *Server) write(nodeID uuid.UUID, frameType byte, payload []byte, addr *net.UDPAddr) {
	s.mutex.Lock()
	conn := s.conn
	s.mutex.Unlock()
	if conn == nil || addr == nil {
		return
	}
	frame := Frame{Type: frameType, NodeID: nodeID, Payload: payload}
	if _, err := conn.WriteToUDP(frame.Encode(), addr); err != nil && s.logger != nil {
		s.logger.Warn("relay write failed", "error", err.Error())
	}
}

// replyError 回一个 ERROR 帧（不泄露失败原因）。
func (s *Server) replyError(nodeID uuid.UUID, addr *net.UDPAddr) {
	s.countPacket("dropped", 1)
	s.write(nodeID, FrameError, nil, addr)
}

func (s *Server) countPacket(direction string, count int) {
	if s.metrics == nil || count <= 0 {
		return
	}
	s.metrics.ObserveRelayPacket(direction, count)
}

func (s *Server) countBytes(count int) {
	if s.metrics == nil || count <= 0 {
		return
	}
	s.metrics.ObserveRelayBytes(count)
}

// Sessions 返回当前会话数（测试与运维观测用）。
func (s *Server) Sessions() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.sessions)
}

// DroppedPackets 返回被丢弃的包数（限流/非法帧）。
func (s *Server) DroppedPackets() uint64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.dropped
}

// SessionDropped 返回某个会话被丢弃的包数。
func (s *Server) SessionDropped(nodeID uuid.UUID) uint64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if entry, ok := s.sessions[nodeID]; ok {
		return entry.dropped
	}
	return 0
}

func addrKey(addr *net.UDPAddr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}
