package mesh

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"neilico/agent/internal/client"
)

// fakeClock 是可注入的假时钟。
type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(duration)
}

// punchRecorder 记录打洞器发出的打洞包与设置过的 endpoint。
type punchRecorder struct {
	mutex     sync.Mutex
	punched   []string
	endpoints []string
}

func (r *punchRecorder) Send(ctx context.Context, address string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.punched = append(r.punched, address)
	return nil
}

func (r *punchRecorder) SetEndpoint(ctx context.Context, publicKey, endpoint string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.endpoints = append(r.endpoints, endpoint)
	return nil
}

func (r *punchRecorder) current() string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.endpoints) == 0 {
		return ""
	}
	return r.endpoints[len(r.endpoints)-1]
}

func (r *punchRecorder) punches() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string{}, r.punched...)
}

func newTestPuncher(clock *fakeClock, recorder *punchRecorder, handshake func(ctx context.Context, key string) (time.Time, error)) *Puncher {
	return NewPuncher(PuncherOptions{
		Now:         clock.Now,
		Send:        recorder.Send,
		SetEndpoint: recorder.SetEndpoint,
		Handshake:   handshake,
		Prefixes: func() []netip.Prefix {
			return []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
		},
	})
}

func TestPunchCandidateOrderPrefersSameSubnetLAN(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return time.Time{}, nil
	})
	peer := PeerCandidates{
		PublicKey: "PEERKEY",
		Candidates: []client.Candidate{
			{Kind: "stun", Addr: "203.0.113.9:51820"},
			{Kind: "public", Addr: "198.51.100.9:51820"},
			{Kind: "lan", Addr: "192.168.1.50:51820"},
			{Kind: "lan", Addr: "10.9.9.9:51820"},
		},
		Endpoint: "203.0.113.9:51820",
	}
	order := puncher.orderCandidates(peer)
	want := []string{"192.168.1.50:51820", "203.0.113.9:51820", "198.51.100.9:51820", "10.9.9.9:51820"}
	if len(order) != len(want) {
		t.Fatalf("候选顺序 = %v，期望 %v", order, want)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("候选顺序 = %v，期望 %v", order, want)
		}
	}
}

func TestPunchCandidateOrderWithoutSameSubnetLAN(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return time.Time{}, nil
	})
	peer := PeerCandidates{
		PublicKey: "PEERKEY",
		Candidates: []client.Candidate{
			{Kind: "lan", Addr: "10.9.9.9:51820"},
			{Kind: "public", Addr: "198.51.100.9:51820"},
			{Kind: "stun", Addr: "203.0.113.9:51820"},
		},
		Endpoint: "198.51.100.9:51820",
	}
	order := puncher.orderCandidates(peer)
	want := []string{"203.0.113.9:51820", "198.51.100.9:51820", "10.9.9.9:51820"}
	if len(order) != len(want) {
		t.Fatalf("候选顺序 = %v，期望 %v", order, want)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("候选顺序 = %v，期望 %v", order, want)
		}
	}
}

func TestPunchSkipsPeersWithoutCandidates(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return time.Time{}, nil
	})
	// 老版本 agent / 老版本控制面：没有候选 → 不能动 endpoint，也不能发打洞包。
	err := puncher.Apply(context.Background(), []PeerCandidates{{PublicKey: "PEERKEY", Endpoint: "1.2.3.4:51820"}})
	if err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if len(recorder.punches()) != 0 {
		t.Fatalf("没有候选时不应发打洞包，实际 %v", recorder.punches())
	}
	if recorder.current() != "" {
		t.Fatalf("没有候选时不应改 endpoint，实际 %s", recorder.current())
	}
}

// 同网段的 LAN 候选直连可达 → 直接用它当 endpoint。
func TestPunchAppliesSameSubnetLANCandidate(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return time.Time{}, nil
	})
	peers := []PeerCandidates{{
		PublicKey: "PEERKEY",
		Candidates: []client.Candidate{
			{Kind: "stun", Addr: "203.0.113.9:51820"},
			{Kind: "lan", Addr: "192.168.1.50:51820"},
		},
		Endpoint: "203.0.113.9:51820",
	}}
	if err := puncher.Apply(context.Background(), peers); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if recorder.current() != "192.168.1.50:51820" {
		t.Fatalf("当前 endpoint = %s，期望同网段的 LAN 候选", recorder.current())
	}
	punches := recorder.punches()
	if len(punches) != 1 || punches[0] != "192.168.1.50:51820" {
		t.Fatalf("打洞包目标 = %v，期望 [192.168.1.50:51820]", punches)
	}
}

// 控制面下发的 endpoint 能握手 → 绝不能换成候选（否则会把能用的隧道弄断）。
func TestPunchKeepsWorkingEndpoint(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	// 健康的隧道：握手时间一直在往前走。
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return clock.Now(), nil
	})
	peers := []PeerCandidates{{
		PublicKey:  "PEERKEY",
		Candidates: []client.Candidate{{Kind: "stun", Addr: "203.0.113.9:51820"}},
		Endpoint:   "198.51.100.9:51820",
	}}
	if err := puncher.Apply(context.Background(), peers); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if recorder.current() != "" {
		t.Fatalf("还没验证之前不应改 endpoint，实际 %s", recorder.current())
	}
	for cycle := 0; cycle < 5; cycle++ {
		clock.Advance(30 * time.Second)
		puncher.evaluate(context.Background())
	}
	if recorder.current() != "" {
		t.Fatalf("握手正常时不应改 endpoint，实际 %s", recorder.current())
	}
	if len(recorder.punches()) != 0 {
		t.Fatalf("握手正常时不应发打洞包，实际 %v", recorder.punches())
	}
}

// 控制面下发的 endpoint 握手不通 → 握手窗口过后按候选顺序换。
func TestPunchRotatesAfterHandshakeTimeout(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return time.Time{}, nil // 永远握手不上
	})
	peers := []PeerCandidates{{
		PublicKey: "PEERKEY",
		Candidates: []client.Candidate{
			{Kind: "stun", Addr: "203.0.113.9:51820"},
			{Kind: "public", Addr: "198.51.100.9:51820"},
		},
		Endpoint: "9.9.9.9:51820",
	}}
	if err := puncher.Apply(context.Background(), peers); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	// 握手窗口（10s）内没有握手 → 换下一个候选。
	clock.Advance(11 * time.Second)
	puncher.evaluate(context.Background())
	if recorder.current() != "203.0.113.9:51820" {
		t.Fatalf("超时后 endpoint = %s，期望 203.0.113.9:51820", recorder.current())
	}
	punches := recorder.punches()
	if len(punches) != 1 || punches[0] != "203.0.113.9:51820" {
		t.Fatalf("打洞包目标 = %v，期望 [203.0.113.9:51820]", punches)
	}
	// 再过一轮 → 换到下一个候选（public）。
	clock.Advance(11 * time.Second)
	puncher.evaluate(context.Background())
	if recorder.current() != "198.51.100.9:51820" {
		t.Fatalf("再次超时后 endpoint = %s，期望 198.51.100.9:51820", recorder.current())
	}
}

// 之前握手成功过，之后连续 3 个周期没有握手 → 换下一个候选。
func TestPunchRotatesAfterThreeCyclesWithoutHandshake(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	handshakeTime := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	working := true
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		if working {
			return handshakeTime, nil
		}
		return time.Time{}, nil
	})
	peers := []PeerCandidates{{
		PublicKey: "PEERKEY",
		Candidates: []client.Candidate{
			{Kind: "stun", Addr: "203.0.113.9:51820"},
			{Kind: "public", Addr: "198.51.100.9:51820"},
		},
		Endpoint: "9.9.9.9:51820",
	}}
	if err := puncher.Apply(context.Background(), peers); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	// 第一次评估：握手成功 → 保持当前 endpoint。
	puncher.evaluate(context.Background())
	if recorder.current() != "" {
		t.Fatalf("握手成功后不应换候选，实际 %s", recorder.current())
	}
	// 之后握手不再更新：连续 3 个周期没有握手才换。
	working = false
	for cycle := 0; cycle < 2; cycle++ {
		clock.Advance(30 * time.Second)
		puncher.evaluate(context.Background())
		if recorder.current() != "" {
			t.Fatalf("第 %d 个周期就换了候选，实际 %s", cycle+1, recorder.current())
		}
	}
	clock.Advance(30 * time.Second)
	puncher.evaluate(context.Background())
	if recorder.current() != "203.0.113.9:51820" {
		t.Fatalf("连续 3 个周期无握手后应换候选，实际 %s", recorder.current())
	}
}

// 控制面没给 endpoint（对端还没上报）→ 直接用第一个候选。
func TestPunchUsesFirstCandidateWithoutEndpoint(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recorder := &punchRecorder{}
	puncher := newTestPuncher(clock, recorder, func(context.Context, string) (time.Time, error) {
		return time.Time{}, nil
	})
	peers := []PeerCandidates{{
		PublicKey:  "PEERKEY",
		Candidates: []client.Candidate{{Kind: "stun", Addr: "203.0.113.9:51820"}},
	}}
	if err := puncher.Apply(context.Background(), peers); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	if recorder.current() != "203.0.113.9:51820" {
		t.Fatalf("当前 endpoint = %s，期望 203.0.113.9:51820", recorder.current())
	}
}
