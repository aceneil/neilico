package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"neilico/agent/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// 心跳采集候选时不能被 STUN 探测拖住（服务器不通时一次要等好几秒）。
func TestCandidateCollectorDoesNotBlockHeartbeat(t *testing.T) {
	// 指向一个不会回包的本地端口：探测必然超时。
	t.Setenv("NEILICO_AGENT_STUN_SERVERS", "127.0.0.1:9")
	collector := newCandidateCollector(config.Config{}, testLogger())
	collector.listenPort = 51820
	started := time.Now()
	candidates := collector.candidates(context.Background())
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("采集候选耗时 %s，心跳不能被 STUN 探测拖慢", elapsed)
	}
	for _, candidate := range candidates {
		if candidate.Kind != "lan" && candidate.Kind != "stun" && candidate.Kind != "public" {
			t.Fatalf("未知的候选种类：%+v", candidate)
		}
		if candidate.Addr == "" {
			t.Fatalf("候选地址为空：%+v", candidate)
		}
	}
}

// 内网候选必须是「内网地址 + WireGuard 监听端口」。
func TestCandidateCollectorLANAddresses(t *testing.T) {
	collector := newCandidateCollector(config.Config{}, testLogger())
	collector.listenPort = 51820
	for _, candidate := range collector.lanCandidates() {
		if candidate.Kind != "lan" {
			t.Fatalf("候选种类不对：%+v", candidate)
		}
		host, port, err := net.SplitHostPort(candidate.Addr)
		if err != nil {
			t.Fatalf("候选地址格式不对 %q: %v", candidate.Addr, err)
		}
		if host == "" {
			t.Fatalf("候选地址缺少主机部分：%+v", candidate)
		}
		if port != strconv.Itoa(collector.listenPort) {
			t.Fatalf("候选端口 = %s，期望 %d", port, collector.listenPort)
		}
	}
}
