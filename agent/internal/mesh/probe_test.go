package mesh

import (
	"context"
	"strings"
	"testing"

	"neilico/agent/internal/client"
)

// 探测器用可注入的假 executor，完全不依赖 root 权限或真实内核网络。

func TestSystemProbeReportsMissingInterface(t *testing.T) {
	// 无任何命令输出（接口不存在）→ 报告接口缺失，并短路不再查对端/路由。
	probe := NewSystemProbe(&recordingExecutor{outputs: map[string]string{}})
	missing, err := probe.Missing(context.Background(), Config{
		Interface: "wg0",
		Peers:     []client.Peer{{PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", AllowedIPs: []string{"100.64.0.3/32"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "interface wg0 missing") {
		t.Fatalf("应仅报告接口缺失，实际缺失项：%v", missing)
	}
}

func TestSystemProbeReportsNoDriftWhenEverythingPresent(t *testing.T) {
	const peerKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	probe := NewSystemProbe(&recordingExecutor{outputs: map[string]string{
		"ip link show dev wg0":                "wg0: <POINTOPOINT,UP>",
		"wg show wg0 dump":                    "PRIVKEY\tPUBKEY\t51820\toff\n" + peerKey + "\t(none)\t1.2.3.4:51820\t100.64.0.3/32\t0\t0\t0\toff",
		"ip route show 100.64.0.3/32 dev wg0": "100.64.0.3 dev wg0 scope link",
	}})
	missing, err := probe.Missing(context.Background(), Config{
		Interface: "wg0",
		Peers:     []client.Peer{{PublicKey: peerKey, AllowedIPs: []string{"100.64.0.3/32"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("实物与期望一致时不应报告漂移，实际：%v", missing)
	}
}

func TestSystemProbeReportsMissingPeerAndRoute(t *testing.T) {
	const peerKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	probe := NewSystemProbe(&recordingExecutor{outputs: map[string]string{
		"ip link show dev wg0": "wg0: <POINTOPOINT,UP>",
		// wg dump 只剩接口行：对端消失了。
		"wg show wg0 dump": "PRIVKEY\tPUBKEY\t51820\toff",
		// 对端路由也不在（空输出）。
		"ip route show 100.64.0.3/32 dev wg0": "",
	}})
	missing, err := probe.Missing(context.Background(), Config{
		Interface: "wg0",
		Peers:     []client.Peer{{PublicKey: peerKey, AllowedIPs: []string{"100.64.0.3/32"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(missing, "; ")
	if !strings.Contains(joined, "peer ") {
		t.Fatalf("应报告对端缺失，实际：%v", missing)
	}
	if !strings.Contains(joined, "route 100.64.0.3/32 dev wg0 missing") {
		t.Fatalf("应报告对端路由缺失，实际：%v", missing)
	}
}

// 对端在、但 AllowedIPs 不一致（被改小/改没）→ 必须报告，这是"配置不一致"的核心检查。
func TestSystemProbeReportsAllowedIPsMismatch(t *testing.T) {
	const peerKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	probe := NewSystemProbe(&recordingExecutor{outputs: map[string]string{
		"ip link show dev wg0": "wg0: <POINTOPOINT,UP>",
		// 内核里该 peer 的 AllowedIPs 只剩一半（少了 192.168.50.0/24）。
		"wg show wg0 dump":                      "PRIVKEY\tPUBKEY\t51820\toff\n" + peerKey + "\t(none)\t1.2.3.4:51820\t100.64.0.3/32\t0\t0\t0\toff",
		"ip route show 100.64.0.3/32 dev wg0":   "100.64.0.3 dev wg0 scope link",
		"ip route show 192.168.50.0/24 dev wg0": "",
	}})
	missing, err := probe.Missing(context.Background(), Config{
		Interface: "wg0",
		Peers:     []client.Peer{{PublicKey: peerKey, AllowedIPs: []string{"100.64.0.3/32", "192.168.50.0/24"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(missing, "; ")
	if !strings.Contains(joined, "allowed_ips 192.168.50.0/24 missing") {
		t.Fatalf("应报告缺失的 AllowedIPs，实际：%v", missing)
	}
	if strings.Contains(joined, "100.64.0.3/32") {
		t.Fatalf("存在的 AllowedIPs 不应被误报为缺失：%v", missing)
	}
}
