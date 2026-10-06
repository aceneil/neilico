//go:build linux

package capabilities

import (
	"context"
	"fmt"
	"os"
	"time"

	"neilico/agent/internal/route"
)

// defaultInterfaceProbe 是 Linux 上的真实探测：创建再删除一个一次性 WireGuard
// 接口。成功即证明本机同时具备内核 WireGuard 能力、`ip` 工具与相应的
// CAP_NET_ADMIN 权限——足以承载 mesh 隧道。
var defaultInterfaceProbe = probeWireGuardInterface

// probeWireGuardInterface 创建（随后删除）一个名字唯一的一次性接口，避免与真实
// 接口（wg0）以及并发探测互相干扰。无论成功失败都会尽力清理，不留残留。
func probeWireGuardInterface() error {
	// Linux 接口名上限 15 字节："neilico-p" (9) + 6 位十六进制足以保持唯一。
	name := fmt.Sprintf("neilico-p%06x", os.Getpid()&0xffffff)
	executor := route.ExecExecutor{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 清理上次探测可能残留的同名接口，避免「File exists」造成假阴性。
	_ = executor.Run(ctx, route.Command{Name: "ip", Args: []string{"link", "delete", "dev", name}})
	if err := executor.Run(ctx, route.Command{Name: "ip", Args: []string{"link", "add", "dev", name, "type", "wireguard"}}); err != nil {
		return err
	}
	// 创建成功即可判定可用；删除失败不应把「能建接口」误报成不可用，但残留接口
	// 会污染后续探测，所以尽力清理并忽略错误。
	_ = executor.Run(ctx, route.Command{Name: "ip", Args: []string{"link", "delete", "dev", name}})
	return nil
}
