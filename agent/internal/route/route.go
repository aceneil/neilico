package route

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Command struct {
	Name    string
	Args    []string
	Comment string
}

func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, c.Name)
	for _, arg := range c.Args {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"\\$`") {
			arg = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
		parts = append(parts, arg)
	}
	line := strings.Join(parts, " ")
	if c.Comment != "" {
		return line + "\n# " + c.Comment
	}
	return line
}

type Executor interface {
	Run(context.Context, Command) error
	Output(context.Context, Command) (string, error)
}

type ExecExecutor struct{}

func (ExecExecutor) Run(ctx context.Context, command Command) error {
	output, err := exec.CommandContext(ctx, command.Name, command.Args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%s: %s", command.String(), message)
		}
		return fmt.Errorf("%s: %w", command.String(), err)
	}
	return nil
}

func (ExecExecutor) Output(ctx context.Context, command Command) (string, error) {
	output, err := exec.CommandContext(ctx, command.Name, command.Args...).Output()
	return strings.TrimSpace(string(output)), err
}

type Route struct {
	CIDR string
}

type Options struct {
	Interface         string
	AllowForwarding   bool
	ExternalInterface string
	MeshCIDR          string
}

type stateEntry struct {
	cidr          string
	alreadyExists bool
}

type Manager struct {
	executor             Executor
	options              Options
	added                map[string]stateEntry
	forwardingApplied    bool
	forwardingWasEnabled bool
	masqWasPresent       bool
}

func (m *Manager) SetMeshCIDR(cidr string) {
	if strings.TrimSpace(cidr) != "" {
		m.options.MeshCIDR = strings.TrimSpace(cidr)
	}
}

func NewManager(executor Executor, options Options) *Manager {
	if executor == nil {
		executor = ExecExecutor{}
	}
	return &Manager{executor: executor, options: options, added: make(map[string]stateEntry)}
}

func (m *Manager) Plan(routes []Route) ([]Command, error) {
	var commands []Command
	seen := make(map[string]bool)
	for _, item := range routes {
		if strings.TrimSpace(item.CIDR) == "" {
			return nil, errors.New("route CIDR is empty")
		}
		if seen[item.CIDR] {
			continue
		}
		seen[item.CIDR] = true
		commands = append(commands, Command{Name: "ip", Args: []string{"route", "add", item.CIDR, "dev", m.options.Interface}})
	}
	if len(routes) > 0 && m.options.AllowForwarding {
		commands = append(commands,
			Command{Name: "sysctl", Args: []string{"-w", "net.ipv4.ip_forward=1"}},
			Command{Name: "iptables", Args: []string{"-t", "nat", "-A", "POSTROUTING", "-s", m.options.MeshCIDR, "-o", m.options.ExternalInterface, "-j", "MASQUERADE"}},
		)
	}
	return commands, nil
}

func (m *Manager) Apply(ctx context.Context, routes []Route) ([]Command, error) {
	planned, err := m.Plan(routes)
	if err != nil {
		return nil, err
	}
	for _, item := range routes {
		if _, exists := m.added[item.CIDR]; exists {
			continue
		}
		exists, err := m.routeExists(ctx, item.CIDR)
		if err != nil {
			return planned, err
		}
		if exists {
			m.added[item.CIDR] = stateEntry{cidr: item.CIDR, alreadyExists: true}
			continue
		}
		command := Command{Name: "ip", Args: []string{"route", "add", item.CIDR, "dev", m.options.Interface}}
		if err := m.executor.Run(ctx, command); err != nil {
			return planned, err
		}
		m.added[item.CIDR] = stateEntry{cidr: item.CIDR}
	}
	if len(routes) > 0 && m.options.AllowForwarding && !m.forwardingApplied {
		if err := m.applyForwarding(ctx); err != nil {
			return planned, err
		}
	}
	return planned, nil
}

func (m *Manager) applyForwarding(ctx context.Context) error {
	if strings.TrimSpace(m.options.ExternalInterface) == "" || strings.TrimSpace(m.options.MeshCIDR) == "" {
		return errors.New("forwarding requires mesh network CIDR and external interface")
	}
	output, err := m.executor.Output(ctx, Command{Name: "sysctl", Args: []string{"-n", "net.ipv4.ip_forward"}})
	if err != nil {
		return fmt.Errorf("read current ip_forward state: %w", err)
	}
	m.forwardingWasEnabled = strings.TrimSpace(output) == "1"
	if err := m.executor.Run(ctx, Command{Name: "sysctl", Args: []string{"-w", "net.ipv4.ip_forward=1"}}); err != nil {
		return err
	}
	masq := Command{Name: "iptables", Args: []string{"-t", "nat", "-C", "POSTROUTING", "-s", m.options.MeshCIDR, "-o", m.options.ExternalInterface, "-j", "MASQUERADE"}}
	if err := m.executor.Run(ctx, masq); err == nil {
		m.masqWasPresent = true
	} else {
		add := Command{Name: "iptables", Args: []string{"-t", "nat", "-A", "POSTROUTING", "-s", m.options.MeshCIDR, "-o", m.options.ExternalInterface, "-j", "MASQUERADE"}}
		if err := m.executor.Run(ctx, add); err != nil {
			return err
		}
	}
	m.forwardingApplied = true
	return nil
}

func (m *Manager) routeExists(ctx context.Context, cidr string) (bool, error) {
	output, err := m.executor.Output(ctx, Command{Name: "ip", Args: []string{"route", "show", cidr}})
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (m *Manager) Cleanup(ctx context.Context) error {
	var failures []string
	for _, item := range m.added {
		if item.alreadyExists {
			continue
		}
		command := Command{Name: "ip", Args: []string{"route", "del", item.cidr, "dev", m.options.Interface}}
		if err := m.executor.Run(ctx, command); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if m.forwardingApplied && !m.masqWasPresent {
		command := Command{Name: "iptables", Args: []string{"-t", "nat", "-D", "POSTROUTING", "-s", m.options.MeshCIDR, "-o", m.options.ExternalInterface, "-j", "MASQUERADE"}}
		if err := m.executor.Run(ctx, command); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if m.forwardingApplied && !m.forwardingWasEnabled {
		command := Command{Name: "sysctl", Args: []string{"-w", "net.ipv4.ip_forward=0"}}
		if err := m.executor.Run(ctx, command); err != nil {
			failures = append(failures, err.Error())
		}
	}
	m.added = make(map[string]stateEntry)
	m.forwardingApplied = false
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
