package mesh

// 打洞（UDP hole punching）：双方都在 NAT 后面时，靠互相发 UDP 包让 NAT 建立
// 映射，再由 WireGuard 自己的握手把隧道拉起来。
//
// 几条硬约束（都是踩过坑才写下来的）：
//   - **绝不发 WireGuard 握手**：打洞包是普通 UDP 随机载荷。手工构造握手报文会
//     污染 wg 状态（重放计数器、密钥轮换），失败时也没法回滚。
//   - **不改 ApplyConfig 的签名**：打洞是「收到配置之后」的可选步骤；没有候选
//     （老版本 agent / 老版本控制面）时，一个字节都不动，行为与上线前完全一致。
//   - **候选顺序固定**：同网段 LAN → STUN → public → LAN（跨网段）→ 控制面 endpoint。

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"neilico/agent/internal/client"
)

const (
	// punchInterval 是重新评估对端 endpoint 的周期。
	punchInterval = 30 * time.Second
	// punchHandshakeTimeout 是「换完候选后多久内必须看到握手」的时间窗。
	punchHandshakeTimeout = 10 * time.Second
	// punchMaxFailures 是已连通的 endpoint 连续多少个周期没有握手就换候选。
	punchMaxFailures = 3
	// punchPackets 是每个候选发送的打洞包数量。
	punchPackets = 3
	// punchPacketInterval 是打洞包之间的间隔。
	punchPacketInterval = 200 * time.Millisecond
	// punchPayloadSize 是打洞包载荷长度（16 字节随机数）。
	punchPayloadSize = 16
)

// PeerCandidates 是一个对端的候选地址集合。
type PeerCandidates struct {
	// PublicKey 是对端的 WireGuard 公钥（用来改它的 endpoint）。
	PublicKey string
	// NodeID 是对端的节点 ID（仅用于日志）。
	NodeID string
	// Candidates 是对端上报的候选地址（stun / public / lan）。
	Candidates []client.Candidate
	// Endpoint 是控制面下发的默认 endpoint，作为候选全失败时的兜底。
	Endpoint string
}

// PeerCandidatesFromPeers 把控制面下发的对端列表转成打洞用的候选集合。
func PeerCandidatesFromPeers(peers []client.Peer) []PeerCandidates {
	result := make([]PeerCandidates, 0, len(peers))
	for _, peer := range peers {
		result = append(result, PeerCandidates{
			PublicKey:  peer.PublicKey,
			NodeID:     peer.NodeID,
			Candidates: peer.Candidates,
			Endpoint:   peer.Endpoint,
		})
	}
	return result
}

// PuncherOptions 是打洞器的可注入依赖（单测里全部可以替换成假实现）。
type PuncherOptions struct {
	// Send 向候选地址发送打洞包；默认是 UDP 随机载荷。
	Send func(ctx context.Context, address string) error
	// SetEndpoint 把候选写进 WireGuard 对端的 endpoint；默认走 wgctrl。
	SetEndpoint func(ctx context.Context, publicKey, endpoint string) error
	// Handshake 返回对端最近一次握手时间；默认走 wgctrl。
	Handshake func(ctx context.Context, publicKey string) (time.Time, error)
	// AllHandshakes 判断本机与**所有**对端是否都有新鲜的握手；默认走 wgctrl。
	// 没有配置时返回 false（调用方按「判断不出来」处理）。
	AllHandshakes func(ctx context.Context) (bool, error)
	// Prefixes 返回本机内网网段（判断对端的 LAN 候选是否同网段）。
	Prefixes func() []netip.Prefix
	// Logger 是日志出口。
	Logger *slog.Logger
	// Now 是时钟（单测可注入假时钟）。
	Now func() time.Time
	// Interval 是重新评估周期，默认 30s。
	Interval time.Duration
	// HandshakeTimeout 是握手时间窗，默认 10s。
	HandshakeTimeout time.Duration
	// MaxFailures 是连续失败多少次换候选，默认 3。
	MaxFailures int
}

type punchState struct {
	key           string
	order         []string
	current       string
	fallback      string
	tried         map[string]bool
	lastHandshake time.Time
	deadline      time.Time
	failures      int
	confirmed     bool
	initialized   bool
}

// Puncher 按候选顺序给每个对端挑 endpoint，并周期性重新评估。
type Puncher struct {
	options PuncherOptions
	mutex   sync.Mutex
	peers   map[string]*punchState
	synced  bool
}

// NewPuncher 构造打洞器。缺省依赖会被补上（UDP 打洞包 + wgctrl）。
func NewPuncher(options PuncherOptions) *Puncher {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Interval <= 0 {
		options.Interval = punchInterval
	}
	if options.HandshakeTimeout <= 0 {
		options.HandshakeTimeout = punchHandshakeTimeout
	}
	if options.MaxFailures <= 0 {
		options.MaxFailures = punchMaxFailures
	}
	if options.Send == nil {
		options.Send = UDPPunch
	}
	return &Puncher{options: options, peers: map[string]*punchState{}}
}

// Apply 按候选顺序给每个对端选一个 endpoint，并发打洞包。
//
// 没有候选的对端（老版本 agent / 老版本控制面）会被直接跳过——它的 endpoint
// 保持控制面下发的原样，行为与打洞功能上线前完全一致。
func (p *Puncher) Apply(ctx context.Context, peers []PeerCandidates) error {
	p.mutex.Lock()
	p.synced = true
	p.mutex.Unlock()
	var failures []error
	for _, peer := range peers {
		if err := p.applyPeer(ctx, peer); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// AllPeersHandshaking 判断本机与所有对端是否都建立了（新鲜的）直连握手。
//
// 没有对端时返回 true——没有 peer 就谈不上直连不通。
// 判断不出来（wgctrl 不可用）时返回 false，调用方据此**不上报** direct_path。
func (p *Puncher) AllPeersHandshaking(ctx context.Context) (bool, error) {
	if p.options.AllHandshakes == nil {
		return false, errors.New("未配置握手探测")
	}
	return p.options.AllHandshakes(ctx)
}

// NeedsPeers 报告打洞器是否还没有拿到过对端信息。
//
// 配置没变化时 agent 会拿到 304（不重新应用配置），但打洞器仍然需要对端信息，
// 才能在隧道不通时切换候选——所以这时需要补拉一次最新配置。
func (p *Puncher) NeedsPeers() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return !p.synced
}

// Run 周期性重新评估：握手成功就留着当前 endpoint，连续 3 个周期没有握手就换
// 下一个候选。ctx 结束时返回。
func (p *Puncher) Run(ctx context.Context) {
	ticker := time.NewTicker(p.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.evaluate(ctx)
		}
	}
}

func (p *Puncher) applyPeer(ctx context.Context, peer PeerCandidates) error {
	key := strings.TrimSpace(peer.PublicKey)
	if key == "" {
		return nil
	}
	if len(peer.Candidates) == 0 {
		// 没有候选（老版本 agent / 老版本控制面）→ 完全不动这个对端的 endpoint，
		// 行为与打洞功能上线前完全一致。
		return nil
	}
	order := p.orderCandidates(peer)
	fallback := strings.TrimSpace(peer.Endpoint)
	lan := p.sameSubnetLAN(peer)

	p.mutex.Lock()
	state, ok := p.peers[key]
	if !ok {
		state = &punchState{key: key, tried: map[string]bool{}}
		p.peers[key] = state
	}
	first := !state.initialized
	state.initialized = true
	state.order = order
	changed := state.fallback != fallback
	state.fallback = fallback
	if changed || first {
		// 第一次见到这个对端（或控制面下发的 endpoint 变了）：从它开始试。
		// **先不动 wg 里的 endpoint**——它可能是可用的（既有行为），贸然换成候选
		// 反而会把能用的隧道弄断。等握手确认它不通了再按候选顺序换。
		state.current = fallback
		state.deadline = p.options.Now().Add(p.options.HandshakeTimeout)
		state.confirmed = false
		state.failures = 0
		state.tried = map[string]bool{}
	}
	target := ""
	if first && lan != "" && lan != state.current {
		// 同网段的 LAN 候选：直连可达，直接用它当 endpoint（与 reconciler 里
		// PreferLANEndpoints 的既有偏好一致）。
		target = lan
	}
	if target == "" && state.current == "" && len(order) > 0 {
		// 控制面没给 endpoint（对端还没上报）→ 直接用第一个候选。
		target = order[0]
	}
	p.mutex.Unlock()
	if target == "" {
		return nil
	}
	return p.activate(ctx, key, target)
}

// sameSubnetLAN 返回与本机同一网段的 LAN 候选（没有则返回空）。
func (p *Puncher) sameSubnetLAN(peer PeerCandidates) string {
	for _, candidate := range peer.Candidates {
		if strings.ToLower(strings.TrimSpace(candidate.Kind)) != "lan" {
			continue
		}
		address := strings.TrimSpace(candidate.Addr)
		if address == "" {
			continue
		}
		if p.sameSubnet(address) {
			return address
		}
	}
	return ""
}

// activate 启用一个候选：先发打洞包（让 NAT 建映射），再把候选写进 WireGuard
// 对端的 endpoint（让 WireGuard 自己的握手去验证它）。
func (p *Puncher) activate(ctx context.Context, key, address string) error {
	if address == "" {
		return nil
	}
	if p.options.Send != nil {
		if err := p.options.Send(ctx, address); err != nil {
			p.log(slog.LevelWarn, "打洞包发送失败", "peer", shortKey(key), "candidate", address, "error", err)
		}
	}
	if err := p.setEndpoint(ctx, key, address); err != nil {
		return err
	}
	p.mutex.Lock()
	if state, ok := p.peers[key]; ok {
		state.current = address
		state.deadline = p.options.Now().Add(p.options.HandshakeTimeout)
		state.failures = 0
		state.confirmed = false
		state.tried[address] = true
	}
	p.mutex.Unlock()
	return nil
}

func (p *Puncher) setEndpoint(ctx context.Context, key, address string) error {
	if p.options.SetEndpoint == nil {
		return nil
	}
	if err := p.options.SetEndpoint(ctx, key, address); err != nil {
		p.log(slog.LevelWarn, "设置对端 endpoint 失败", "peer", shortKey(key), "candidate", address, "error", err)
		return fmt.Errorf("设置对端 %s 的 endpoint 为 %s 失败: %w", shortKey(key), address, err)
	}
	return nil
}

// evaluate 重新评估所有对端：握手有更新就留着，连续失败就换候选。
func (p *Puncher) evaluate(ctx context.Context) {
	p.mutex.Lock()
	snapshot := make([]*punchState, 0, len(p.peers))
	for _, state := range p.peers {
		snapshot = append(snapshot, state)
	}
	p.mutex.Unlock()

	rotate := make([]string, 0, len(snapshot))
	for _, state := range snapshot {
		if state.current == "" {
			continue
		}
		handshake, err := p.handshake(ctx, state.key)
		now := p.options.Now()
		if err == nil && handshake.After(state.lastHandshake) {
			p.mutex.Lock()
			state.lastHandshake = handshake
			state.confirmed = true
			state.failures = 0
			p.mutex.Unlock()
			continue
		}
		if !state.confirmed {
			// 刚换的候选还没验证：握手窗口内没有握手 → 换下一个。
			p.mutex.Lock()
			expired := now.After(state.deadline)
			p.mutex.Unlock()
			if expired {
				rotate = append(rotate, state.key)
			}
			continue
		}
		// 之前握手成功过，现在连续若干周期没有新握手 → 换下一个候选。
		p.mutex.Lock()
		state.failures++
		failures := state.failures
		p.mutex.Unlock()
		if failures >= p.options.MaxFailures {
			p.mutex.Lock()
			state.failures = 0
			p.mutex.Unlock()
			rotate = append(rotate, state.key)
		}
	}
	for _, key := range rotate {
		p.rotate(ctx, key)
	}
}

// rotate 换到下一个还没试过的候选；都试过了就回到控制面下发的 endpoint。
func (p *Puncher) rotate(ctx context.Context, key string) {
	p.mutex.Lock()
	state, ok := p.peers[key]
	if !ok {
		p.mutex.Unlock()
		return
	}
	next := ""
	for _, candidate := range state.order {
		if candidate == state.current || state.tried[candidate] {
			continue
		}
		next = candidate
		break
	}
	if next == "" {
		// 候选都试过了：回到控制面下发的 endpoint（保持既有行为），下一轮从头再来。
		state.tried = map[string]bool{}
		switch {
		case state.fallback != "" && state.fallback != state.current:
			next = state.fallback
		case len(state.order) > 0:
			next = state.order[0]
		}
	}
	current := state.current
	p.mutex.Unlock()
	if next == "" || next == current {
		return
	}
	p.log(slog.LevelInfo, "切换到下一个候选", "peer", shortKey(key), "from", current, "to", next)
	if err := p.activate(ctx, key, next); err != nil {
		p.log(slog.LevelWarn, "切换候选失败", "peer", shortKey(key), "candidate", next, "error", err)
	}
}

// orderCandidates 给出候选的尝试顺序：
//
//  1. 与本机同一网段的 LAN 候选（直连可达，最优先）；
//  2. STUN 探测到的公网映射地址；
//  3. public 候选（ipify 探测的出口地址）；
//  4. 其余 LAN 候选（跨网段，多半不通但值得一试）；
//  5. 控制面下发的 endpoint（兜底）。
func (p *Puncher) orderCandidates(peer PeerCandidates) []string {
	byKind := map[string][]string{}
	for _, candidate := range peer.Candidates {
		address := strings.TrimSpace(candidate.Addr)
		if address == "" {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(candidate.Kind))
		if kind == "" {
			kind = "public"
		}
		byKind[kind] = append(byKind[kind], address)
	}
	ordered := make([]string, 0, 8)
	appendAll := func(values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || containsString(ordered, value) {
				continue
			}
			ordered = append(ordered, value)
		}
	}
	// 1) 同网段的 LAN 候选优先。
	for _, address := range byKind["lan"] {
		if p.sameSubnet(address) {
			appendAll([]string{address})
			break
		}
	}
	// 2) stun → 3) public → 4) lan
	appendAll(byKind["stun"])
	appendAll(byKind["public"])
	appendAll(byKind["lan"])
	// 5) 控制面下发的 endpoint 兜底。
	appendAll([]string{peer.Endpoint})
	return ordered
}

// sameSubnet 判断候选地址是否落在本机某个内网网段里。
func (p *Puncher) sameSubnet(address string) bool {
	host := address
	if parsed, _, err := net.SplitHostPort(address); err == nil {
		host = parsed
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	if p.options.Prefixes == nil {
		return false
	}
	for _, prefix := range p.options.Prefixes() {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *Puncher) handshake(ctx context.Context, key string) (time.Time, error) {
	if p.options.Handshake == nil {
		return time.Time{}, errors.New("未配置握手探测")
	}
	return p.options.Handshake(ctx, key)
}

func (p *Puncher) log(level slog.Level, message string, args ...any) {
	if p.options.Logger == nil {
		return
	}
	p.options.Logger.Log(context.Background(), level, message, args...)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// UDPPunch 向候选地址发送 3 个 16 字节随机载荷的 UDP 包（间隔 200ms）。
//
// 目的只是让双方的 NAT 建立映射；**不构造 WireGuard 握手报文**，以免污染 wg 状态。
func UDPPunch(ctx context.Context, address string) error {
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return fmt.Errorf("解析候选地址 %s 失败: %w", address, err)
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return fmt.Errorf("连接候选地址 %s 失败: %w", address, err)
	}
	defer conn.Close()
	payload := make([]byte, punchPayloadSize)
	if _, err := rand.Read(payload); err != nil {
		return fmt.Errorf("生成打洞载荷失败: %w", err)
	}
	for index := 0; index < punchPackets; index++ {
		if index > 0 {
			timer := time.NewTimer(punchPacketInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if _, err := conn.Write(payload); err != nil {
			return fmt.Errorf("发送打洞包到 %s 失败: %w", address, err)
		}
	}
	return nil
}

// WGPuncher 用 wgctrl 操作 WireGuard 对端：改 endpoint、读最近握手时间。
type WGPuncher struct {
	client        *wgctrl.Client
	interfaceName string
}

// NewWGPuncher 打开 wgctrl 客户端（失败时返回错误，调用方降级为「只发打洞包」）。
func NewWGPuncher(interfaceName string) (*WGPuncher, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	return &WGPuncher{client: client, interfaceName: interfaceName}, nil
}

// SetEndpoint 只改对端的 endpoint，不动 AllowedIPs / PSK 等其它字段
// （不设置 ReplacePeers，避免把别的对端删掉）。
func (w *WGPuncher) SetEndpoint(ctx context.Context, publicKey, endpoint string) error {
	key, err := wgtypes.ParseKey(strings.TrimSpace(publicKey))
	if err != nil {
		return fmt.Errorf("解析对端公钥失败: %w", err)
	}
	address, err := netip.ParseAddrPort(strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("候选地址 %s 不是合法的 ip:port: %w", endpoint, err)
	}
	return w.client.ConfigureDevice(w.interfaceName, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{{PublicKey: key, Endpoint: net.UDPAddrFromAddrPort(address)}},
	})
}

// Handshake 返回对端最近一次握手时间（从未握手过时是零值）。
func (w *WGPuncher) Handshake(ctx context.Context, publicKey string) (time.Time, error) {
	device, err := w.client.Device(w.interfaceName)
	if err != nil {
		return time.Time{}, err
	}
	for _, peer := range device.Peers {
		if peer.PublicKey.String() == strings.TrimSpace(publicKey) {
			return peer.LastHandshakeTime, nil
		}
	}
	return time.Time{}, nil
}

// PeerState 返回对端当前的 endpoint 与最近一次握手时间。
//
// endpoint 是空字符串表示对端还没有 endpoint（WireGuard 会按 AllowedIPs 里的
// 目标自己学习）。
func (w *WGPuncher) PeerState(ctx context.Context, publicKey string) (string, time.Time, error) {
	device, err := w.client.Device(w.interfaceName)
	if err != nil {
		return "", time.Time{}, err
	}
	for _, peer := range device.Peers {
		if peer.PublicKey.String() != strings.TrimSpace(publicKey) {
			continue
		}
		endpoint := ""
		if peer.Endpoint != nil {
			endpoint = peer.Endpoint.String()
		}
		return endpoint, peer.LastHandshakeTime, nil
	}
	return "", time.Time{}, nil
}

// AllPeersHandshaking 判断设备上所有对端是否都有新鲜的握手。
//
// 「新鲜」的判定用 DirectPathWindow：WireGuard 的重协商周期是 120 秒，握手时间戳
// 在重协商之前不会刷新，所以窗口必须大于 120 秒，否则会把好好的隧道判成断线。
func (w *WGPuncher) AllPeersHandshaking(ctx context.Context) (bool, error) {
	device, err := w.client.Device(w.interfaceName)
	if err != nil {
		return false, err
	}
	if len(device.Peers) == 0 {
		return true, nil
	}
	for _, peer := range device.Peers {
		if peer.LastHandshakeTime.IsZero() {
			return false, nil
		}
		if time.Since(peer.LastHandshakeTime) > DirectPathWindow {
			return false, nil
		}
	}
	return true, nil
}
