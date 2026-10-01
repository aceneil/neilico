package validation

import (
	"net/netip"
	"testing"
)

func TestNetworkCIDR(t *testing.T) {
	tests := []struct {
		value string
		want  string
		error bool
	}{
		{value: "100.64.0.0/24", want: "100.64.0.0/24"},
		{value: " 10.1.2.3/17 ", want: "10.1.0.0/17"},
		{value: "100.64.0.0/16", error: true},
		{value: "10.0.0.0/8", error: true},
		{value: "2001:db8::/64", error: true},
		{value: "not-a-prefix", error: true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := NetworkCIDR(test.value)
			if test.error {
				if err == nil {
					t.Fatalf("NetworkCIDR(%q) unexpectedly succeeded", test.value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != test.want {
				t.Fatalf("NetworkCIDR(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestNetworkVirtualIP(t *testing.T) {
	prefix := netip.MustParsePrefix("100.64.1.0/24")
	for _, value := range []string{"100.64.1.2", "100.64.1.254"} {
		if _, err := NetworkVirtualIP(prefix, value); err != nil {
			t.Fatalf("NetworkVirtualIP(%q): %v", value, err)
		}
	}
	for _, value := range []string{"100.64.1.0", "100.64.1.1", "100.64.1.255", "100.64.2.2", "bad"} {
		if _, err := NetworkVirtualIP(prefix, value); err == nil {
			t.Fatalf("NetworkVirtualIP(%q) unexpectedly succeeded", value)
		}
	}
}

func TestRouteCIDRNormalizesIPv4(t *testing.T) {
	got, err := RouteCIDR("192.168.1.42/24")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "192.168.1.0/24" {
		t.Fatalf("RouteCIDR() = %q", got)
	}
	if _, err := RouteCIDR("2001:db8::/64"); err == nil {
		t.Fatal("RouteCIDR accepted IPv6")
	}
}
