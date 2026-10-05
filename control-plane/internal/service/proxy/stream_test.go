package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 端口分配说明：这些测试要的是一个「确定的监听端口」，所以只能先 listen :0 拿空闲端口、
// 关掉、再让转发器监听同一个端口 —— 这中间存在被抢占的窗口。同包内其它测试（以及本机
// 其它进程）都可能抢到它，因此统一走 applyRule()：取端口→启动→检查状态，失败就换端口重试。
// 不要给这些测试加 t.Parallel()（会让窗口更容易被兄弟测试命中）。

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("分配空闲端口失败: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	socket, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("分配空闲 UDP 端口失败: %v", err)
	}
	port := socket.LocalAddr().(*net.UDPAddr).Port
	socket.Close()
	return port
}

// applyRule 在空闲端口上启动规则，返回绑定成功的规则（含实际监听端口）。
// 单条规则启动失败（端口被抢）只影响它自己，这里换端口重试，避免测试偶发失败。
func applyRule(t *testing.T, forwarder *StreamForwarder, rule StreamRule, protocol string) StreamRule {
	t.Helper()
	for attempt := 0; attempt < 10; attempt++ {
		if protocol == "udp" {
			rule.ListenPort = freeUDPPort(t)
		} else {
			rule.ListenPort = freeTCPPort(t)
		}
		forwarder.Apply(context.Background(), []StreamRule{rule})
		if stats, ok := forwarder.Stats()[rule.ID]; ok && stats.Status == "running" {
			return rule
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("重试 10 次仍无法为 %s 规则拿到可用端口（持续被占用）", protocol)
	return rule
}

func tcpEchoServer(t *testing.T) (host string, port int, stop func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 echo 服务失败: %v", err)
	}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(connection)
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return address.IP.String(), address.Port, func() { listener.Close() }
}

func TestStreamTCPForwardsTraffic(t *testing.T) {
	upstreamHost, upstreamPort, stop := tcpEchoServer(t)
	defer stop()

	forwarder := NewStreamForwarder(nil, StreamForwarderOptions{})
	defer forwarder.Close()
	ruleID := uuid.New()
	rule := applyRule(t, forwarder, StreamRule{
		ID: ruleID, Name: "echo", Protocol: "tcp",
		TargetHost: upstreamHost, TargetPort: upstreamPort,
	}, "tcp")

	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", rule.ListenPort), 3*time.Second)
	if err != nil {
		t.Fatalf("连接转发端口失败: %v", err)
	}
	defer connection.Close()

	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(response) != "ping" {
		t.Fatalf("回显 = %q，期望 ping", response)
	}

	// 字节计数是边转发边累加的，但写入完成与该请求线程观测之间仍有微秒级间隔 → 轮询等待。
	var stats StreamStats
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats = forwarder.Stats()[ruleID]
		if stats.BytesIn >= 4 && stats.BytesOut >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stats.Status != "running" {
		t.Fatalf("状态 = %q，期望 running", stats.Status)
	}
	if stats.BytesIn < 4 || stats.BytesOut < 4 {
		t.Fatalf("字节统计异常（长连接期间也应累加）: in=%d out=%d", stats.BytesIn, stats.BytesOut)
	}
}

func TestStreamTCPWhitelistDeniesUnlistedPeer(t *testing.T) {
	upstreamHost, upstreamPort, stop := tcpEchoServer(t)
	defer stop()

	forwarder := NewStreamForwarder(nil, StreamForwarderOptions{})
	defer forwarder.Close()
	rule := applyRule(t, forwarder, StreamRule{
		ID: uuid.New(), Protocol: "tcp",
		TargetHost: upstreamHost, TargetPort: upstreamPort,
		IPWhitelist: []string{"198.51.100.0/24"},
	}, "tcp")

	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", rule.ListenPort), 3*time.Second)
	if err != nil {
		t.Fatalf("连接转发端口失败: %v", err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("白名单外的来源不应收到任何数据（连接应被立即关闭）")
	}
}

func TestStreamUDPForwardsDatagrams(t *testing.T) {
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("启动 UDP echo 失败: %v", err)
	}
	defer upstream.Close()
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, addr, err := upstream.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = upstream.WriteToUDP(buffer[:n], addr)
		}
	}()

	forwarder := NewStreamForwarder(nil, StreamForwarderOptions{UDPIdle: 2 * time.Second})
	defer forwarder.Close()
	ruleID := uuid.New()
	upstreamAddress := upstream.LocalAddr().(*net.UDPAddr)
	rule := applyRule(t, forwarder, StreamRule{
		ID: ruleID, Protocol: "udp",
		TargetHost: upstreamAddress.IP.String(), TargetPort: upstreamAddress.Port,
	}, "udp")

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: rule.ListenPort})
	if err != nil {
		t.Fatalf("连接 UDP 转发端口失败: %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("datagram")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	response := make([]byte, 16)
	n, err := client.Read(response)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(response[:n]) != "datagram" {
		t.Fatalf("UDP 回显 = %q，期望 datagram", response[:n])
	}
	if stats := forwarder.Stats()[ruleID]; stats.BytesIn < 8 || stats.BytesOut < 8 {
		t.Fatalf("UDP 字节统计异常: %+v", stats)
	}
}

func TestStreamApplyRemovesDeletedRule(t *testing.T) {
	upstreamHost, upstreamPort, stop := tcpEchoServer(t)
	defer stop()

	forwarder := NewStreamForwarder(nil, StreamForwarderOptions{})
	defer forwarder.Close()
	applyRule(t, forwarder, StreamRule{
		ID: uuid.New(), Protocol: "tcp",
		TargetHost: upstreamHost, TargetPort: upstreamPort,
	}, "tcp")
	if len(forwarder.Stats()) != 1 {
		t.Fatal("期望 1 条运行中规则")
	}

	forwarder.Apply(context.Background(), nil)
	if len(forwarder.Stats()) != 0 {
		t.Fatal("删除后不应残留规则")
	}
}

func TestStreamApplyReportsPortConflict(t *testing.T) {
	upstreamHost, upstreamPort, stop := tcpEchoServer(t)
	defer stop()

	forwarder := NewStreamForwarder(nil, StreamForwarderOptions{})
	defer forwarder.Close()
	first := uuid.New()
	rule := applyRule(t, forwarder, StreamRule{
		ID: first, Protocol: "tcp",
		TargetHost: upstreamHost, TargetPort: upstreamPort,
	}, "tcp")

	// 同一协议 + 同一端口再来一条：第二条必须失败，且不影响第一条。
	second := uuid.New()
	forwarder.Apply(context.Background(), []StreamRule{
		rule,
		{ID: second, Protocol: "tcp", ListenPort: rule.ListenPort, TargetHost: upstreamHost, TargetPort: upstreamPort},
	})
	stats := forwarder.Stats()
	if stats[first].Status != "running" {
		t.Fatalf("第一条应为 running，实际 %q（%s）", stats[first].Status, stats[first].LastError)
	}
	if stats[second].Status != "error" {
		t.Fatalf("端口冲突的第二条应为 error，实际 %q", stats[second].Status)
	}
	if stats[second].LastError == "" {
		t.Fatal("端口冲突应记录原因")
	}
}
