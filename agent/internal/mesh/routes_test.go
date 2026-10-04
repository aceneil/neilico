package mesh

import (
	"context"
	"os"
	"strings"
	"testing"

	"neilico/agent/internal/client"
	"neilico/agent/internal/route"
)

// 回归护栏：每个对端的 AllowedIPs 都必须有对应的 `ip route replace ... dev <iface>`。
// 缺了它，包会走默认网关（物理网卡）→ 握手成功但数据一个字节都过不去（真机实测）。
func TestPeerRouteCommandsCoverAllowedIPs(t *testing.T) {
	config := Config{
		Interface:  "wg0",
		ListenPort: 51820,
		Peers: []client.Peer{
			{PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", AllowedIPs: []string{"100.64.0.2/32", "192.168.50.0/24"}},
			{PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", AllowedIPs: []string{"100.64.0.3/32"}},
			{PublicKey: "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=", AllowedIPs: []string{"100.64.0.2/32"}}, // 重复的 AllowedIP
		},
	}
	commands := PeerRouteCommands(config)
	got := make([]string, 0, len(commands))
	for _, command := range commands {
		// 只比较命令本体（command.String() 会带上注释后缀）
		got = append(got, strings.Join(append([]string{command.Name}, command.Args...), " "))
	}
	want := []string{
		"ip route replace 100.64.0.2/32 dev wg0",
		"ip route replace 192.168.50.0/24 dev wg0",
		"ip route replace 100.64.0.3/32 dev wg0",
	}
	if len(got) != len(want) {
		t.Fatalf("路由命令数 = %d，期望 %d：%v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("第 %d 条 = %q，期望 %q", index, got[index], want[index])
		}
	}
	if PeerRouteCommands(Config{Interface: "wg0"}) != nil {
		t.Fatal("没有对端时不应产生路由命令")
	}
	if PeerRouteCommands(Config{Peers: config.Peers}) != nil {
		t.Fatal("没有接口名时不应产生路由命令")
	}
}

// 应用顺序必须是：建链/配地址 → wg setconf → 加路由。顺序错了路由会加在不存在的接口上。
func TestShellApplyAddsRoutesAfterSetconf(t *testing.T) {
	content, err := os.ReadFile("testdata/wireguard.conf")
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{outputs: map[string]string{"ip link show dev wg0": "wg0: <POINTOPOINT>"}}
	applier := NewShellApplier(executor, t.TempDir())
	err = applier.Apply(context.Background(), Config{
		Interface:       "wg0",
		MTU:             1420,
		ListenPort:      51820,
		WireGuardConfig: string(content),
		Peers: []client.Peer{{
			PublicKey:  "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
			AllowedIPs: []string{"100.64.0.2/32"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	setconfAt, routeAt := -1, -1
	for index, command := range executor.commands {
		if command.Name == "wg" && len(command.Args) > 0 && command.Args[0] == "setconf" {
			setconfAt = index
		}
		if command.Name == "ip" && len(command.Args) > 1 && command.Args[0] == "route" && command.Args[1] == "replace" {
			if routeAt < 0 {
				routeAt = index
			}
		}
	}
	if setconfAt < 0 {
		t.Fatalf("未执行 wg setconf：%v", commandStrings(executor.commands))
	}
	if routeAt < 0 {
		t.Fatalf("未添加对端路由（这正是 握手成功但数据不通 的病因）：%v", commandStrings(executor.commands))
	}
	if routeAt < setconfAt {
		t.Fatalf("路由必须在 setconf 之后执行：setconf@%d route@%d", setconfAt, routeAt)
	}
	found := false
	for _, command := range executor.commands {
		if strings.Contains(command.String(), "100.64.0.2/32") && strings.Contains(command.String(), "wg0") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 100.64.0.2/32 → wg0 的路由：%v", commandStrings(executor.commands))
	}
}

func commandStrings(commands []route.Command) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		out = append(out, command.String())
	}
	return out
}
