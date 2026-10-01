package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLongestPrefixMatch(t *testing.T) {
	t.Parallel()
	set := &RouteSet{byHost: map[string][]Route{
		"app.example.com": {
			{Host: "app.example.com", Path: "/", Target: "127.0.0.1:8000"},
			{Host: "app.example.com", Path: "/api/v1", Target: "127.0.0.1:8001"},
			{Host: "app.example.com", Path: "/api", Target: "127.0.0.1:8002"},
		},
	}}
	for _, test := range []struct{ path, want string }{
		{"/", "127.0.0.1:8000"},
		{"/api", "127.0.0.1:8002"},
		{"/api/users", "127.0.0.1:8002"},
		{"/api/v1/users", "127.0.0.1:8001"},
	} {
		route := set.Lookup("APP.EXAMPLE.COM:8081", test.path)
		if route == nil || route.Target != test.want {
			t.Fatalf("Lookup(%q) = %#v, want %s", test.path, route, test.want)
		}
	}
}

func TestAllowIP(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	if !allowIP(request.RemoteAddr, nil) {
		t.Fatal("empty whitelist rejected")
	}
	if !allowIP(request.RemoteAddr, []string{"192.0.2.0/24"}) {
		t.Fatal("CIDR whitelist rejected")
	}
	if allowIP(request.RemoteAddr, []string{"198.51.100.1/32"}) {
		t.Fatal("outside IP was allowed")
	}
}
