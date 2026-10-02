package mesh

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"neilico/agent/internal/route"
)

type DryRunApplier struct {
	inner  Applier
	output io.Writer
}

func NewDryRunApplier(inner Applier, output io.Writer) *DryRunApplier {
	if output == nil {
		output = os.Stdout
	}
	return &DryRunApplier{inner: inner, output: output}
}

func (a *DryRunApplier) Apply(ctx context.Context, config Config) error {
	var commands []route.Command
	if planner, ok := a.inner.(interface {
		Plan(Config) ([]route.Command, error)
	}); ok {
		var err error
		commands, err = planner.Plan(config)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(a.output, "=== NEILICO agent dry-run: complete WireGuard configuration ===")
	fmt.Fprint(a.output, config.WireGuardConfig)
	if config.WireGuardConfig != "" && config.WireGuardConfig[len(config.WireGuardConfig)-1] != '\n' {
		fmt.Fprintln(a.output)
	}
	fmt.Fprintln(a.output, "=== commands ===")
	for _, command := range commands {
		fmt.Fprintln(a.output, command.String())
	}
	return nil
}

func (a *DryRunApplier) Cleanup(context.Context) error { return nil }

func NewApplier(dryRun bool, executor route.Executor, tempDir string, output io.Writer, logger *slog.Logger) Applier {
	inner := NewWGOrShell(executor, tempDir, logger)
	if dryRun {
		return NewDryRunApplier(NewShellApplier(executor, tempDir), output)
	}
	return inner
}

var _ Applier = (*DryRunApplier)(nil)
