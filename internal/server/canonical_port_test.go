package server

import (
	"crypto/tls"
	"github.com/uwaserver/uwas/internal/config"
	"net/http/httptest"
	"testing"
)

func canonicalPortLocation(host, uri, pref string, secure bool) string {
	r := httptest.NewRequest("GET", uri, nil)
	r.Host = host
	if secure {
		r.TLS = &tls.ConnectionState{}
	}
	return canonicalRedirectLocation(&config.Domain{Host: "example.com", CanonicalHost: pref}, r)
}
func TestCanonicalRedirectPreservesNondefaultPorts(t *testing.T) {
	for _, c := range []struct {
		name, host, uri, pref, want string
		secure                      bool
	}{
		{"nondefault HTTPS", "example.com:8443", "/docs?q=1", "www", "https://www.example.com:8443/docs?q=1", true},
		{"nondefault HTTP", "www.example.com:8080", "/docs?q=1", "apex", "http://example.com:8080/docs?q=1", false},
		{"default HTTPS", "example.com:443", "/", "www", "https://www.example.com/", true},
		{"default HTTP", "example.com:80", "/", "www", "http://www.example.com/", false},
		{"HTTP on 443", "example.com:443", "/", "www", "http://www.example.com:443/", false},
		{"HTTPS on 80", "example.com:80", "/", "www", "https://www.example.com:80/", true},
		{"already canonical", "www.example.com:8443", "/", "www", "", true},
		{"disabled preference", "example.com:8443", "/", "", "", true},
		{"escaped path query", "example.com:8443", "/a%2Fb?x=%2F", "www", "https://www.example.com:8443/a%2Fb?x=%2F", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := canonicalPortLocation(c.host, c.uri, c.pref, c.secure)
			if got != c.want {
				t.Fatalf("EXPECTED: %q ACTUAL: %q", c.want, got)
			}
		})
	}
}
