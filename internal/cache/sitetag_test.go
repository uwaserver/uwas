package cache

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "example.com", "example.com"},
		{"uppercase", "EXAMPLE.com", "example.com"},
		{"port", "example.com:8080", "example.com"},
		{"uppercase and port", "Example.COM:443", "example.com"},
		{"ipv4 with port", "127.0.0.1:19180", "127.0.0.1"},
		{"ipv6 bracketed", "[::1]", "[::1]"},
		{"ipv6 bracketed with port", "[::1]:8080", "[::1]"},
		{"ipv6 uppercase", "[2001:DB8::1]:443", "[2001:db8::1]"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeHost(tt.in); got != tt.want {
				t.Errorf("NormalizeHost(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSiteTagMatchesCacheKeyHost(t *testing.T) {
	// The whole per-domain purge rests on these two agreeing. If the key
	// normalizes a host one way and the tag another, a purge looks for a tag
	// no store ever wrote — which is exactly the bug this pairing prevents.
	hosts := []string{"example.com", "EXAMPLE.com:8080", "[::1]:443", "127.0.0.1:19180"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/page", nil)
			req.Host = host
			key := GenerateKey(req, nil)

			// Key layout is METHOD|scheme|host|path|...
			parts := strings.Split(key, "|")
			if len(parts) < 3 {
				t.Fatalf("unexpected key layout %q", key)
			}
			keyHost := parts[2]

			tag := SiteTag(host)
			wantTag := "site:" + keyHost
			if tag != wantTag {
				t.Errorf("SiteTag(%q) = %q, but key host is %q (want tag %q)",
					host, tag, keyHost, wantTag)
			}
		})
	}
}
