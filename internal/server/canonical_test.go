package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

var tlsStateStub = tls.ConnectionState{}

func TestCanonicalRedirectLocation(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		reqHost   string
		uri       string
		tls       bool
		wantLoc   string
	}{
		{"www pref, apex request redirects", "www", "example.com", "/a?b=1", true, "https://www.example.com/a?b=1"},
		{"www pref, www request no redirect", "www", "www.example.com", "/a", true, ""},
		{"apex pref, www request redirects", "apex", "www.example.com", "/x", true, "https://example.com/x"},
		{"apex pref, apex request no redirect", "apex", "example.com", "/x", true, ""},
		{"no preference, no redirect", "", "example.com", "/", true, ""},
		{"path and query preserved", "www", "example.com", "/p/q?x=1&y=2", true, "https://www.example.com/p/q?x=1&y=2"},
		{"port stripped from host", "www", "example.com:443", "/", true, "https://www.example.com/"},
		{"http scheme when no TLS", "www", "example.com", "/", false, "http://www.example.com/"},
		{"non-dotted host no redirect", "www", "localhost", "/", true, ""},
		{"already-canonical apex under apex pref", "apex", "example.com", "/", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &config.Domain{Host: "example.com", CanonicalHost: tt.canonical}
			r := httptest.NewRequest(http.MethodGet, tt.uri, nil)
			r.Host = tt.reqHost
			if tt.tls {
				r.TLS = &tlsStateStub
			}
			got := canonicalRedirectLocation(d, r)
			if got != tt.wantLoc {
				t.Errorf("canonicalRedirectLocation = %q, want %q", got, tt.wantLoc)
			}
		})
	}
}
