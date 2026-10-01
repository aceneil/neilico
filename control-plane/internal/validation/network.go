package validation

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// NetworkCIDR normalizes an IPv4 network and rejects prefixes larger than /16.
func NetworkCIDR(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, errors.New("cidr must be a valid CIDR prefix")
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, errors.New("cidr must be IPv4")
	}
	if prefix.Bits() <= 16 {
		return netip.Prefix{}, errors.New("cidr prefix length must be greater than /16")
	}
	prefix = prefix.Masked()
	return prefix, nil
}

// RouteCIDR normalizes a routed IPv4 subnet. It permits /16 because subnet
// routes are explicit, audited entries rather than automatically allocated pools.
func RouteCIDR(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, errors.New("cidr must be a valid CIDR prefix")
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, errors.New("cidr must be IPv4")
	}
	return prefix.Masked(), nil
}

// NetworkVirtualIP verifies an unmasked IPv4 address belongs to the pool and
// is not one of its reserved addresses.
func NetworkVirtualIP(prefix netip.Prefix, value string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}, errors.New("virtual_ip must be an IPv4 address")
	}
	if !addr.Is4() {
		return netip.Addr{}, errors.New("virtual_ip must be IPv4")
	}
	addr = addr.Unmap()
	if !prefix.Contains(addr) {
		return netip.Addr{}, fmt.Errorf("virtual_ip must be inside %s", prefix)
	}
	if IsReservedNetworkIP(prefix, addr) {
		return netip.Addr{}, errors.New("virtual_ip uses a reserved network address")
	}
	return addr, nil
}

func IsReservedNetworkIP(prefix netip.Prefix, addr netip.Addr) bool {
	network := prefix.Masked().Addr()
	broadcast := LastIPv4(prefix)
	gateway := network.Next()
	return addr == network || addr == broadcast || addr == gateway
}

func LastIPv4(prefix netip.Prefix) netip.Addr {
	addr := prefix.Masked().Addr().As4()
	hostBits := 32 - prefix.Bits()
	for bit := 0; bit < hostBits; bit++ {
		octet := 3 - bit/8
		offset := uint(bit % 8)
		addr[octet] |= 1 << offset
	}
	return netip.AddrFrom4(addr)
}
