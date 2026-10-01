package route

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeExecutor struct {
	outputs   map[string]string
	commands  []Command
	runErrors map[string]error
}

func (f *fakeExecutor) Run(_ context.Context, command Command) error {
	f.commands = append(f.commands, command)
	if err := f.runErrors[command.String()]; err != nil {
		return err
	}
	return nil
}

func (f *fakeExecutor) Output(_ context.Context, command Command) (string, error) {
	key := command.String()
	if key == "ip route show 192.168.1.0/24" {
		return f.outputs[key], nil
	}
	if key == "sysctl -n net.ipv4.ip_forward" {
		return "0", nil
	}
	if key == "iptables -t nat -C POSTROUTING -s 100.64.0.0/24 -o eth0 -j MASQUERADE" {
		return "", errors.New("not present")
	}
	return f.outputs[key], nil
}

func TestApplyAndRollbackOnlyOwnChanges(t *testing.T) {
	executor := &fakeExecutor{runErrors: map[string]error{
		"iptables -t nat -C POSTROUTING -s 100.64.0.0/24 -o eth0 -j MASQUERADE": errors.New("not present"),
	}}
	manager := NewManager(executor, Options{
		Interface:         "wg0",
		AllowForwarding:   true,
		ExternalInterface: "eth0",
		MeshCIDR:          "100.64.0.0/24",
	})
	commands, err := manager.Apply(context.Background(), []Route{{CIDR: "192.168.1.0/24"}})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(commands) != 3 {
		t.Fatalf("planned commands = %#v", commands)
	}
	joined := commandList(executor.commands)
	for _, expected := range []string{"ip route add 192.168.1.0/24 dev wg0", "sysctl -w net.ipv4.ip_forward=1", "iptables -t nat -A POSTROUTING"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands omitted %q:\n%s", expected, joined)
		}
	}
	if err := manager.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	joined = commandList(executor.commands)
	for _, expected := range []string{"ip route del 192.168.1.0/24 dev wg0", "sysctl -w net.ipv4.ip_forward=0", "iptables -t nat -D POSTROUTING"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("cleanup omitted %q:\n%s", expected, joined)
		}
	}
}

func TestExistingRouteIsNotRemoved(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{"ip route show 192.168.1.0/24": "192.168.1.0/24 dev wg0"}}
	manager := NewManager(executor, Options{Interface: "wg0"})
	if _, err := manager.Apply(context.Background(), []Route{{CIDR: "192.168.1.0/24"}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(commandList(executor.commands), "ip route del") {
		t.Fatalf("removed pre-existing route: %s", commandList(executor.commands))
	}
}

func commandList(commands []Command) string {
	lines := make([]string, 0, len(commands))
	for _, command := range commands {
		lines = append(lines, command.String())
	}
	return strings.Join(lines, "\n")
}
