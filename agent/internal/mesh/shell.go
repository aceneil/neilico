package mesh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"neilico/agent/internal/route"
)

type ShellApplier struct {
	executor      route.Executor
	created       bool
	interfaceName string
	tempDir       string
}

func NewShellApplier(executor route.Executor, tempDir string) *ShellApplier {
	if executor == nil {
		executor = route.ExecExecutor{}
	}
	return &ShellApplier{executor: executor, tempDir: tempDir}
}

func (a *ShellApplier) Plan(config Config) ([]route.Command, error) {
	if strings.TrimSpace(config.WireGuardConfig) == "" {
		return nil, errors.New("wireguard_config is empty")
	}
	parsed, err := parseWireGuardConfig(config.WireGuardConfig)
	if err != nil {
		return nil, err
	}
	commands := []route.Command{
		{Name: "ip", Args: []string{"link", "add", "dev", config.Interface, "type", "wireguard"}},
	}
	for _, address := range parsed.addresses {
		address = strings.TrimSpace(address)
		if address != "" {
			commands = append(commands, route.Command{Name: "ip", Args: []string{"address", "replace", address, "dev", config.Interface}})
		}
	}
	var peerNotes []string
	for _, peer := range parsed.peers {
		peerNotes = append(peerNotes, "peer "+peer.publicKey+" allowed_ips="+strings.Join(peer.allowedIPs, ","))
	}
	commands = append(commands,
		route.Command{Name: "ip", Args: []string{"link", "set", config.Interface, "mtu", fmt.Sprintf("%d", config.MTU), "up"}},
		route.Command{Name: "wg", Args: []string{"setconf", config.Interface, "/run/neilico-agent/wg.conf"}, Comment: strings.Join(peerNotes, "; ")},
	)
	// 再补"对端 AllowedIPs → 隧道接口"的路由（缺了它握手成功但数据不通，见 PeerRouteCommands）
	commands = append(commands, PeerRouteCommands(config)...)
	return commands, nil
}

// setconfIndex 找出命令序列里 wg setconf 的位置，用于在它之后执行路由命令。
func setconfIndex(commands []route.Command) int {
	for index, command := range commands {
		if command.Name == "wg" && len(command.Args) > 0 && command.Args[0] == "setconf" {
			return index
		}
	}
	return -1
}

func (a *ShellApplier) Apply(ctx context.Context, config Config) error {
	commands, err := a.Plan(config)
	if err != nil {
		return err
	}
	exists, err := a.interfaceExists(ctx, config.Interface)
	if err != nil {
		return err
	}
	start := 0
	if exists {
		start = 1
	} else {
		if err := a.executor.Run(ctx, commands[0]); err != nil {
			return err
		}
		a.created = true
		a.interfaceName = config.Interface
	}
	tempFile, err := a.writeConfig(config.WireGuardConfig)
	if err != nil {
		return err
	}
	defer os.Remove(tempFile)
	index := setconfIndex(commands)
	if index < 0 {
		return errors.New("shell applier: planned commands missing wg setconf")
	}
	for _, command := range commands[start:index] {
		if err := a.executor.Run(ctx, command); err != nil {
			return err
		}
	}
	setconf := route.Command{Name: "wg", Args: []string{"setconf", config.Interface, tempFile}}
	if err := a.executor.Run(ctx, setconf); err != nil {
		return err
	}
	// 接口就绪、peer 配好之后再加路由（顺序不能颠倒）
	for _, command := range commands[index+1:] {
		if err := a.executor.Run(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

func (a *ShellApplier) writeConfig(content string) (string, error) {
	directory := a.tempDir
	if directory == "" {
		directory = os.TempDir()
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create WireGuard temporary directory: %w", err)
	}
	file, err := os.CreateTemp(directory, "neilico-wg-*.conf")
	if err != nil {
		return "", fmt.Errorf("create WireGuard temporary config: %w", err)
	}
	name := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(name)
		return "", fmt.Errorf("secure WireGuard temporary config: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(name)
		return "", fmt.Errorf("write WireGuard temporary config: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("close WireGuard temporary config: %w", err)
	}
	return name, nil
}

func (a *ShellApplier) interfaceExists(ctx context.Context, name string) (bool, error) {
	output, err := a.executor.Output(ctx, route.Command{Name: "ip", Args: []string{"link", "show", "dev", name}})
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(output) != "", nil
}

func (a *ShellApplier) Cleanup(ctx context.Context) error {
	if !a.created {
		return nil
	}
	a.created = false
	return a.executor.Run(ctx, route.Command{Name: "ip", Args: []string{"link", "delete", "dev", a.interfaceName}})
}

var _ Applier = (*ShellApplier)(nil)
