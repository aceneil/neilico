package acl

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
)

type Packet struct {
	SrcIP    netip.Addr
	DstIP    netip.Addr
	Protocol string
	DstPort  uint16
}

// Rule keeps member references separate from literal expressions so Match
// remains deterministic and does not require database access.
type Rule struct {
	Src        string
	Dst        string
	Action     string
	Protocol   string
	Ports      string
	Priority   int
	References map[string][]netip.Prefix
}

type indexedRule struct {
	rule  Rule
	order int
}

// Match evaluates rules in ascending numeric priority and returns the first
// matching action. With no match, network policy is deny by default.
func Match(rules []Rule, packet Packet) Decision {
	indexed := make([]indexedRule, len(rules))
	for i := range rules {
		indexed[i] = indexedRule{rule: rules[i], order: i}
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		if indexed[i].rule.Priority == indexed[j].rule.Priority {
			return indexed[i].order < indexed[j].order
		}
		return indexed[i].rule.Priority < indexed[j].rule.Priority
	})
	for _, candidate := range indexed {
		if matches(candidate.rule, packet) {
			if normalizeAction(candidate.rule.Action) == string(Allow) {
				return Allow
			}
			return Deny
		}
	}
	return Deny
}

func Validate(rule Rule) error {
	if normalizeAction(rule.Action) == "" {
		return fmt.Errorf("action must be allow or deny")
	}
	protocol := normalizeProtocol(rule.Protocol)
	if protocol == "" {
		return fmt.Errorf("protocol must be any, tcp, udp, or icmp")
	}
	if err := validateExpression(rule.Src); err != nil {
		return fmt.Errorf("src: %w", err)
	}
	if err := validateExpression(rule.Dst); err != nil {
		return fmt.Errorf("dst: %w", err)
	}
	if _, err := parsePorts(rule.Ports); err != nil {
		return err
	}
	return nil
}

func matches(rule Rule, packet Packet) bool {
	if !matchesAddress(rule.Src, packet.SrcIP, rule.References) ||
		!matchesAddress(rule.Dst, packet.DstIP, rule.References) {
		return false
	}
	if !matchesProtocol(rule.Protocol, packet.Protocol) {
		return false
	}
	protocol := normalizeProtocol(packet.Protocol)
	if protocol == "icmp" {
		return true
	}
	ports, err := parsePorts(rule.Ports)
	return err == nil && containsPort(ports, packet.DstPort)
}

func matchesProtocol(expression, protocol string) bool {
	expression = normalizeProtocol(expression)
	protocol = normalizeProtocol(protocol)
	return expression == "any" || (expression != "" && expression == protocol)
}

func matchesAddress(expression string, addr netip.Addr, references map[string][]netip.Prefix) bool {
	expression = strings.ToLower(strings.TrimSpace(expression))
	if expression == "" || expression == "any" || expression == "*" {
		return true
	}
	if prefixes, ok := references[canonicalReference(expression)]; ok {
		for _, prefix := range prefixes {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	if strings.Contains(expression, "/") {
		prefix, err := netip.ParsePrefix(expression)
		return err == nil && prefix.Contains(addr)
	}
	parsed, err := netip.ParseAddr(expression)
	return err == nil && parsed == addr
}

func canonicalReference(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "member:")
	value = strings.TrimPrefix(value, "node:")
	value = strings.TrimPrefix(value, "member/")
	value = strings.TrimPrefix(value, "node/")
	return value
}

func validateExpression(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "any" || value == "*" {
		return nil
	}
	if strings.HasPrefix(value, "member:") || strings.HasPrefix(value, "node:") ||
		strings.HasPrefix(value, "member/") || strings.HasPrefix(value, "node/") {
		if canonicalReference(value) == "" {
			return fmt.Errorf("member reference must not be empty")
		}
		return nil
	}
	if strings.Contains(value, "/") {
		if _, err := netip.ParsePrefix(value); err != nil {
			return fmt.Errorf("invalid CIDR %q", value)
		}
		return nil
	}
	if _, err := netip.ParseAddr(value); err != nil {
		return fmt.Errorf("must be any, an IP address, a CIDR, or a member reference")
	}
	return nil
}

func normalizeAction(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == string(Allow) || value == string(Deny) {
		return value
	}
	return ""
}

func normalizeProtocol(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "any", "*":
		return "any"
	case "tcp", "udp", "icmp":
		return value
	default:
		return ""
	}
}

type portRange struct {
	start uint16
	end   uint16
}

func parsePorts(value string) ([]portRange, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "any" || value == "*" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	ranges := make([]portRange, 0, len(parts))
	for _, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, fmt.Errorf("ports contains an empty entry")
		}
		bounds := strings.Split(raw, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("invalid port range %q", raw)
		}
		start, err := parsePort(bounds[0])
		if err != nil {
			return nil, err
		}
		end := start
		if len(bounds) == 2 {
			end, err = parsePort(bounds[1])
			if err != nil {
				return nil, err
			}
		}
		if start > end {
			return nil, fmt.Errorf("port range %q is reversed", raw)
		}
		ranges = append(ranges, portRange{start: start, end: end})
	}
	return ranges, nil
}

func parsePort(value string) (uint16, error) {
	value = strings.TrimSpace(value)
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %q must be between 1 and 65535", value)
	}
	return uint16(port), nil
}

func containsPort(ranges []portRange, port uint16) bool {
	if ranges == nil {
		return true
	}
	for _, candidate := range ranges {
		if port >= candidate.start && port <= candidate.end {
			return true
		}
	}
	return false
}
