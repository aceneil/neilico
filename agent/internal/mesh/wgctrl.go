package mesh

import (
	"context"
	"fmt"
	"log/slog"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"umpp/agent/internal/route"
)

type WGCtrlApplier struct {
	client        *wgctrl.Client
	executor      route.Executor
	created       bool
	interfaceName string
}

func NewWGOrShell(executor route.Executor, tempDir string, logger *slog.Logger) Applier {
	wgClient, err := wgctrl.New()
	if err != nil {
		if logger != nil {
			logger.Warn("wgctrl unavailable; using shell WireGuard applier", "reason", err.Error())
		}
		return NewShellApplier(executor, tempDir)
	}
	if executor == nil {
		executor = route.ExecExecutor{}
	}
	return &WGCtrlApplier{client: wgClient, executor: executor}
}

func NewWGCtrl(client *wgctrl.Client, executor route.Executor) *WGCtrlApplier {
	if executor == nil {
		executor = route.ExecExecutor{}
	}
	return &WGCtrlApplier{client: client, executor: executor}
}

func (a *WGCtrlApplier) Apply(ctx context.Context, config Config) error {
	parsed, err := parseWireGuardConfig(config.WireGuardConfig)
	if err != nil {
		return err
	}
	wgSettings, err := wgConfig(parsed, true)
	if err != nil {
		return err
	}
	exists, err := interfaceExists(ctx, a.executor, config.Interface)
	if err != nil {
		return err
	}
	if !exists {
		create := route.Command{Name: "ip", Args: []string{"link", "add", "dev", config.Interface, "type", "wireguard"}}
		if err := a.executor.Run(ctx, create); err != nil {
			return err
		}
		a.created = true
		a.interfaceName = config.Interface
	}
	for _, address := range parsed.addresses {
		command := route.Command{Name: "ip", Args: []string{"address", "replace", address, "dev", config.Interface}}
		if err := a.executor.Run(ctx, command); err != nil {
			return err
		}
	}
	if err := a.client.ConfigureDevice(config.Interface, wgSettings); err != nil {
		return fmt.Errorf("configure WireGuard with wgctrl: %w", err)
	}
	link := route.Command{Name: "ip", Args: []string{"link", "set", config.Interface, "mtu", fmt.Sprintf("%d", config.MTU), "up"}}
	return a.executor.Run(ctx, link)
}

func (a *WGCtrlApplier) Cleanup(ctx context.Context) error {
	if !a.created {
		return nil
	}
	a.created = false
	return a.executor.Run(ctx, route.Command{Name: "ip", Args: []string{"link", "delete", "dev", a.interfaceName}})
}

func interfaceExists(ctx context.Context, executor route.Executor, name string) (bool, error) {
	output, err := executor.Output(ctx, route.Command{Name: "ip", Args: []string{"link", "show", "dev", name}})
	if err != nil {
		return false, nil
	}
	return len(output) > 0, nil
}

var _ Applier = (*WGCtrlApplier)(nil)
var _ = wgtypes.Key{}
