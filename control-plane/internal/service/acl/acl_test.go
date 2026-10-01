package acl

import (
	"net/netip"
	"testing"
)

func TestMatchTable(t *testing.T) {
	memberPrefix := netip.MustParsePrefix("10.0.0.7/32")
	references := map[string][]netip.Prefix{
		"member:node-b": {memberPrefix},
		"node-b":        {memberPrefix},
	}
	rule := func(action, src, dst, protocol, ports string, priority int) Rule {
		return Rule{
			Action: action, Src: src, Dst: dst, Protocol: protocol, Ports: ports,
			Priority: priority, References: references,
		}
	}
	packet := func(src, dst, protocol string, port uint16) Packet {
		return Packet{
			SrcIP: netip.MustParseAddr(src), DstIP: netip.MustParseAddr(dst),
			Protocol: protocol, DstPort: port,
		}
	}
	tests := []struct {
		name   string
		rules  []Rule
		packet Packet
		want   Decision
	}{
		{"default deny empty", nil, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Deny},
		{"explicit allow any", []Rule{rule("allow", "any", "any", "any", "any", 100)}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Allow},
		{"explicit deny any", []Rule{rule("deny", "any", "any", "any", "any", 100)}, packet("10.0.0.1", "10.0.0.2", "udp", 53), Deny},
		{"single source exact", []Rule{rule("allow", "10.0.0.1", "any", "any", "any", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Allow},
		{"single source mismatch", []Rule{rule("allow", "10.0.0.9", "any", "any", "any", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Deny},
		{"single destination exact", []Rule{rule("allow", "any", "10.0.0.2", "any", "any", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Allow},
		{"CIDR source inside", []Rule{rule("allow", "10.0.0.0/24", "any", "any", "any", 1)}, packet("10.0.0.254", "10.1.0.2", "tcp", 80), Allow},
		{"CIDR source lower boundary", []Rule{rule("allow", "10.0.0.0/24", "any", "any", "any", 1)}, packet("10.0.0.0", "10.1.0.2", "tcp", 80), Allow},
		{"CIDR source upper boundary", []Rule{rule("allow", "10.0.0.0/24", "any", "any", "any", 1)}, packet("10.0.0.255", "10.1.0.2", "tcp", 80), Allow},
		{"CIDR source outside", []Rule{rule("allow", "10.0.0.0/24", "any", "any", "any", 1)}, packet("10.0.1.0", "10.1.0.2", "tcp", 80), Deny},
		{"CIDR destination inside", []Rule{rule("allow", "any", "10.1.0.0/24", "any", "any", 1)}, packet("10.0.0.1", "10.1.0.20", "tcp", 80), Allow},
		{"CIDR destination outside", []Rule{rule("allow", "any", "10.1.0.0/24", "any", "any", 1)}, packet("10.0.0.1", "10.2.0.20", "tcp", 80), Deny},
		{"tcp exact port", []Rule{rule("allow", "any", "any", "tcp", "443", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 443), Allow},
		{"tcp wrong port", []Rule{rule("allow", "any", "any", "tcp", "443", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Deny},
		{"port list first", []Rule{rule("allow", "any", "any", "tcp", "80,443", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Allow},
		{"port list second", []Rule{rule("allow", "any", "any", "tcp", "80,443", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 443), Allow},
		{"port list mismatch", []Rule{rule("allow", "any", "any", "tcp", "80,443", 1)}, packet("10.0.0.1", "10.0.0.2", "tcp", 22), Deny},
		{"range lower boundary", []Rule{rule("allow", "any", "any", "udp", "1000-2000", 1)}, packet("10.0.0.1", "10.0.0.2", "udp", 1000), Allow},
		{"range upper boundary", []Rule{rule("allow", "any", "any", "udp", "1000-2000", 1)}, packet("10.0.0.1", "10.0.0.2", "udp", 2000), Allow},
		{"range below boundary", []Rule{rule("allow", "any", "any", "udp", "1000-2000", 1)}, packet("10.0.0.1", "10.0.0.2", "udp", 999), Deny},
		{"range above boundary", []Rule{rule("allow", "any", "any", "udp", "1000-2000", 1)}, packet("10.0.0.1", "10.0.0.2", "udp", 2001), Deny},
		{"protocol mismatch", []Rule{rule("allow", "any", "any", "tcp", "53", 1)}, packet("10.0.0.1", "10.0.0.2", "udp", 53), Deny},
		{"icmp ignores port", []Rule{rule("allow", "any", "any", "icmp", "1-65535", 1)}, packet("10.0.0.1", "10.0.0.2", "icmp", 0), Allow},
		{"priority lower wins deny", []Rule{
			rule("allow", "any", "any", "any", "any", 20),
			rule("deny", "any", "any", "any", "any", 10),
		}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Deny},
		{"priority lower wins allow", []Rule{
			rule("deny", "any", "any", "any", "any", 20),
			rule("allow", "any", "any", "any", "any", 10),
		}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Allow},
		{"first matching rule wins", []Rule{
			rule("deny", "any", "any", "any", "any", 1),
			rule("allow", "any", "any", "any", "any", 1),
		}, packet("10.0.0.1", "10.0.0.2", "tcp", 80), Deny},
		{"member source reference", []Rule{rule("allow", "member:node-b", "any", "any", "any", 1)}, packet("10.0.0.7", "10.0.0.2", "tcp", 80), Allow},
		{"member destination reference", []Rule{rule("allow", "any", "node-b", "any", "any", 1)}, packet("10.0.0.1", "10.0.0.7", "tcp", 80), Allow},
		{"member reference mismatch", []Rule{rule("allow", "member:node-b", "any", "any", "any", 1)}, packet("10.0.0.6", "10.0.0.2", "tcp", 80), Deny},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Match(test.rules, test.packet); got != test.want {
				t.Fatalf("Match() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMatchDoesNotMutateRuleOrder(t *testing.T) {
	rules := []Rule{
		{Action: "allow", Src: "any", Dst: "any", Protocol: "any", Ports: "any", Priority: 20},
		{Action: "deny", Src: "any", Dst: "any", Protocol: "any", Ports: "any", Priority: 10},
	}
	_ = Match(rules, Packet{SrcIP: netip.MustParseAddr("10.0.0.1"), DstIP: netip.MustParseAddr("10.0.0.2")})
	if rules[0].Priority != 20 || rules[1].Priority != 10 {
		t.Fatalf("Match mutated input order: %#v", rules)
	}
}
