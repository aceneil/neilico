package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// StreamRule 是转发引擎运行时看到的规则：目标已解析成可拨号的 host:port。
// 解析（node → 虚拟 IP）在服务层完成，引擎本身只负责转发，便于单测。
type StreamRule struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Name        string
	Protocol    string // tcp | udp
	ListenPort  int
	TargetHost  string
	TargetPort  int
	IPWhitelist []string
}

// Address 返回可拨号的目标地址。
func (r StreamRule) Address() string {
	return net.JoinHostPort(r.TargetHost, fmt.Sprint(r.TargetPort))
}

// StreamStats 是单条规则的运行状态，供 API/UI 展示。
type StreamStats struct {
	Status            string `json:"status"` // running | pending | error
	LastError         string `json:"last_error,omitempty"`
	ActiveConnections int64  `json:"active_connections"`
	BytesIn           int64  `json:"bytes_in"`
	BytesOut          int64  `json:"bytes_out"`
}

// StreamObserver 接收转发指标（可为 nil）。
type StreamObserver interface {
	ObserveStreamConnection(protocol, result string)
	ObserveStreamBytes(protocol string, in, out int64)
}

// StreamForwarder 按规则维护一组监听器：TCP 用 net.Listen + io.Copy（Linux 上走 splice），
// UDP 用会话表 + 空闲超时（不引入 nginx 之类的外部依赖）。
type StreamForwarder struct {
	logger      *slog.Logger
	observer    StreamObserver
	dialTimeout time.Duration
	udpIdle     time.Duration
	drainWait   time.Duration

	mu     sync.Mutex
	active map[uuid.UUID]*streamListener
	closed bool
}

type StreamForwarderOptions struct {
	DialTimeout time.Duration
	UDPIdle     time.Duration
	DrainWait   time.Duration
	Observer    StreamObserver
}

func NewStreamForwarder(logger *slog.Logger, opts StreamForwarderOptions) *StreamForwarder {
	if logger == nil {
		logger = slog.Default()
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.UDPIdle <= 0 {
		opts.UDPIdle = 2 * time.Minute
	}
	if opts.DrainWait <= 0 {
		opts.DrainWait = 30 * time.Second
	}
	return &StreamForwarder{
		logger:      logger,
		observer:    opts.Observer,
		dialTimeout: opts.DialTimeout,
		udpIdle:     opts.UDPIdle,
		drainWait:   opts.DrainWait,
		active:      make(map[uuid.UUID]*streamListener),
	}
}

// Apply 让运行中的监听器与期望规则集合一致：配置变了的重建，删掉的关闭，新增的启动。
// 单条规则启动失败（例如端口被占）只影响它自己，不影响其余规则。
func (f *StreamForwarder) Apply(ctx context.Context, rules []StreamRule) {
	wanted := make(map[uuid.UUID]StreamRule, len(rules))
	for _, rule := range rules {
		wanted[rule.ID] = rule
	}

	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	for id, listener := range f.active {
		rule, keep := wanted[id]
		if keep && listener.sameConfig(rule) {
			delete(wanted, id)
			continue
		}
		listener.close()
		delete(f.active, id)
	}
	f.mu.Unlock()

	for id, rule := range wanted {
		listener, err := f.start(ctx, rule)
		if err != nil {
			f.logger.Warn("stream rule start failed", "rule_id", id, "protocol", rule.Protocol,
				"listen_port", rule.ListenPort, "error", err)
			failed := &streamListener{
				f: f, rule: rule, startedAt: time.Now().UTC(),
				status: "error", lastError: err.Error(),
			}
			f.mu.Lock()
			f.active[id] = failed
			f.mu.Unlock()
			continue
		}
		f.logger.Info("stream rule started", "rule_id", id, "protocol", rule.Protocol,
			"listen_port", rule.ListenPort, "target", rule.Address())
		f.mu.Lock()
		f.active[id] = listener
		f.mu.Unlock()
	}
}

// Stats 返回当前所有规则的运行状态快照。
func (f *StreamForwarder) Stats() map[uuid.UUID]StreamStats {
	f.mu.Lock()
	listeners := make([]*streamListener, 0, len(f.active))
	for _, listener := range f.active {
		listeners = append(listeners, listener)
	}
	f.mu.Unlock()

	stats := make(map[uuid.UUID]StreamStats, len(listeners))
	for _, listener := range listeners {
		in, out := listener.bytesIn.Load(), listener.bytesOut.Load()
		if f.observer != nil && (in > 0 || out > 0) {
			f.observer.ObserveStreamBytes(listener.rule.Protocol, in, out)
		}
		stats[listener.rule.ID] = StreamStats{
			Status:            listener.status,
			LastError:         listener.lastError,
			ActiveConnections: listener.activeConns.Load(),
			BytesIn:           in,
			BytesOut:          out,
		}
	}
	return stats
}

// Close 关闭所有监听器（进程退出时用）。
func (f *StreamForwarder) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	listeners := make([]*streamListener, 0, len(f.active))
	for _, listener := range f.active {
		listeners = append(listeners, listener)
	}
	f.active = make(map[uuid.UUID]*streamListener)
	f.mu.Unlock()
	for _, listener := range listeners {
		listener.close()
	}
}

func (f *StreamForwarder) start(ctx context.Context, rule StreamRule) (*streamListener, error) {
	listener := &streamListener{
		f: f, rule: rule, status: "running", startedAt: time.Now().UTC(),
		udpSessions: make(map[string]*udpSession),
	}
	switch rule.Protocol {
	case "udp":
		address := &net.UDPAddr{IP: net.IPv4zero, Port: rule.ListenPort}
		socket, err := net.ListenUDP("udp", address)
		if err != nil {
			return nil, err
		}
		target, err := net.ResolveUDPAddr("udp", rule.Address())
		if err != nil {
			socket.Close()
			return nil, err
		}
		listener.udp, listener.udpTarget = socket, target
		listener.wg.Add(1)
		go listener.udpLoop()
	default:
		socket, err := net.Listen("tcp", fmt.Sprintf(":%d", rule.ListenPort))
		if err != nil {
			return nil, err
		}
		listener.tcp = socket
		listener.wg.Add(1)
		go listener.acceptLoop()
	}
	return listener, nil
}

// countingWriter 统计已转发的字节数。
//
// 取舍说明：包一层会使 io.Copy 失去 splice 零拷贝快路径（内核级转发），换来的是
// 「连接未结束也能实时看到字节数」。本产品入口带宽受公网链路限制，用户态拷贝的
// 数 GB/s 远高于链路带宽，因此可观测性更值钱；若将来更看重裸吞吐，可改回原始
// io.Copy 并把字节数改为连接结束时一次性统计。
type countingWriter struct {
	writer io.Writer
	total  *atomic.Int64
}

func (w countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.total.Add(int64(n))
	return n, err
}

type streamListener struct {
	f           *StreamForwarder
	rule        StreamRule
	tcp         net.Listener
	udp         *net.UDPConn
	udpTarget   *net.UDPAddr
	udpMu       sync.Mutex
	udpSessions map[string]*udpSession
	wg          sync.WaitGroup
	closed      atomic.Bool
	activeConns atomic.Int64
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64
	startedAt   time.Time
	status      string
	lastError   string
}

func (l *streamListener) sameConfig(rule StreamRule) bool {
	return l.rule.Protocol == rule.Protocol &&
		l.rule.ListenPort == rule.ListenPort &&
		l.rule.TargetHost == rule.TargetHost &&
		l.rule.TargetPort == rule.TargetPort &&
		sameStringSet(l.rule.IPWhitelist, rule.IPWhitelist)
}

func (l *streamListener) close() {
	if l.closed.Swap(true) {
		return
	}
	if l.tcp != nil {
		l.tcp.Close()
	}
	if l.udp != nil {
		l.udp.Close()
	}
	l.udpMu.Lock()
	for key, session := range l.udpSessions {
		session.conn.Close()
		delete(l.udpSessions, key)
	}
	l.udpMu.Unlock()
}

func (l *streamListener) acceptLoop() {
	defer l.wg.Done()
	for {
		connection, err := l.tcp.Accept()
		if err != nil {
			if !l.closed.Load() {
				l.recordError(err)
			}
			return
		}
		tcpConn, ok := connection.(*net.TCPConn)
		if !ok {
			connection.Close()
			continue
		}
		if !allowIP(tcpConn.RemoteAddr().String(), l.rule.IPWhitelist) {
			l.observe("denied")
			tcpConn.Close()
			continue
		}
		l.wg.Add(1)
		go l.handleTCP(tcpConn)
	}
}

func (l *streamListener) handleTCP(client *net.TCPConn) {
	defer l.wg.Done()
	upstream, err := net.DialTimeout("tcp", l.rule.Address(), l.f.dialTimeout)
	if err != nil {
		l.observe("failed")
		// 单次连接拨号失败【不等于】规则本身坏了：监听器仍在正常accept，
		// 只是目标此刻不可达（如对端离线）。只记 last_error，状态保持 running，
		// 否则 UI 会误报「错误」。
		l.lastError = err.Error()
		l.f.logger.Warn("stream connection failed", "rule_id", l.rule.ID,
			"listen_port", l.rule.ListenPort, "target", l.rule.Address(), "error", err)
		client.Close()
		return
	}
	l.observe("opened")
	l.activeConns.Add(1)
	defer l.activeConns.Add(-1)

	server, ok := upstream.(*net.TCPConn)
	if !ok {
		client.Close()
		upstream.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(countingWriter{writer: server, total: &l.bytesIn}, client)
		server.CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(countingWriter{writer: client, total: &l.bytesOut}, server)
		client.CloseWrite()
		done <- struct{}{}
	}()
	<-done
	// 半关闭语义：一个方向结束（例如客户端发完 EOF）后，给另一方向留收尾时间。
	select {
	case <-done:
	case <-time.After(l.f.drainWait):
	}
	client.Close()
	server.Close()
}

func (l *streamListener) udpLoop() {
	defer l.wg.Done()
	buffer := make([]byte, 65535)
	for {
		n, clientAddr, err := l.udp.ReadFromUDP(buffer)
		if err != nil {
			if !l.closed.Load() {
				l.recordError(err)
			}
			return
		}
		if !allowIP(clientAddr.String(), l.rule.IPWhitelist) {
			l.observe("denied")
			continue
		}
		session, err := l.session(clientAddr)
		if err != nil {
			l.observe("failed")
			l.recordError(err)
			continue
		}
		_ = session.conn.SetReadDeadline(time.Now().Add(l.f.udpIdle))
		if _, err := session.conn.Write(buffer[:n]); err != nil {
			l.dropSession(clientAddr, session)
			l.recordError(err)
			continue
		}
		l.bytesIn.Add(int64(n))
	}
}

type udpSession struct {
	client *net.UDPAddr
	conn   *net.UDPConn
}

func (l *streamListener) session(clientAddr *net.UDPAddr) (*udpSession, error) {
	key := clientAddr.String()
	l.udpMu.Lock()
	if session, ok := l.udpSessions[key]; ok {
		l.udpMu.Unlock()
		return session, nil
	}
	l.udpMu.Unlock()

	outbound, err := net.DialUDP("udp", nil, l.udpTarget)
	if err != nil {
		return nil, err
	}
	session := &udpSession{client: clientAddr, conn: outbound}
	l.udpMu.Lock()
	if existing, ok := l.udpSessions[key]; ok {
		l.udpMu.Unlock()
		outbound.Close()
		return existing, nil
	}
	l.udpSessions[key] = session
	l.udpMu.Unlock()

	l.observe("opened")
	l.activeConns.Add(1)
	l.wg.Add(1)
	go l.udpResponseLoop(clientAddr, session)
	return session, nil
}

func (l *streamListener) udpResponseLoop(clientAddr *net.UDPAddr, session *udpSession) {
	defer l.wg.Done()
	buffer := make([]byte, 65535)
	for {
		// 读超时同时充当空闲回收：对端静默超过 udpIdle 即结束会话。
		_ = session.conn.SetReadDeadline(time.Now().Add(l.f.udpIdle))
		n, err := session.conn.Read(buffer)
		if err != nil {
			l.dropSession(clientAddr, session)
			return
		}
		if _, err := l.udp.WriteToUDP(buffer[:n], clientAddr); err != nil {
			l.dropSession(clientAddr, session)
			return
		}
		l.bytesOut.Add(int64(n))
	}
}

func (l *streamListener) dropSession(clientAddr *net.UDPAddr, session *udpSession) {
	l.udpMu.Lock()
	if current, ok := l.udpSessions[clientAddr.String()]; ok && current == session {
		delete(l.udpSessions, clientAddr.String())
	}
	l.udpMu.Unlock()
	session.conn.Close()
	l.activeConns.Add(-1)
}

func (l *streamListener) recordError(err error) {
	l.lastError = err.Error()
	l.status = "error"
	l.f.logger.Warn("stream rule error", "rule_id", l.rule.ID, "protocol", l.rule.Protocol,
		"listen_port", l.rule.ListenPort, "error", err)
}

func (l *streamListener) observe(result string) {
	if l.f.observer != nil {
		l.f.observer.ObserveStreamConnection(l.rule.Protocol, result)
	}
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}
