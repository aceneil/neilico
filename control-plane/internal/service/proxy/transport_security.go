package proxy

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	acmepkg "umpp/control-plane/internal/service/cert/acme"
)

func SecurityHeaders(next http.Handler, hstsMaxAge int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && hstsMaxAge > 0 {
			w.Header().Set("Strict-Transport-Security", "max-age="+strconv.Itoa(hstsMaxAge))
		}
		next.ServeHTTP(w, r)
	})
}

func RedirectHTTP(next http.Handler, tlsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if isRedirectExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
			replace := port
			if tlsPort != "" {
				if _, tlsPortNumber, splitErr := net.SplitHostPort(tlsPort); splitErr == nil {
					replace = tlsPortNumber
				} else {
					replace = strings.TrimPrefix(tlsPort, ":")
				}
			}
			host = net.JoinHostPort(hostnameOnly(host), replace)
		}
		status := http.StatusMovedPermanently
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			status = http.StatusPermanentRedirect
		}
		location := "https://" + host + r.URL.RequestURI()
		w.Header().Set("Location", location)
		w.WriteHeader(status)
	})
}

func isRedirectExempt(path string) bool {
	return path == "/healthz" || path == "/metrics" || acmepkg.IsChallengePath(path)
}

func hostnameOnly(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return host
	}
	return strings.Trim(hostPort, "[]")
}
