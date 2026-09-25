package cache

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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

// keyPart returns the n-th (0-based) length-prefixed component of a
// generateKey key: <len>:METHOD<len>:scheme<len>:host<len>:path...
// Returns "" when the key does not hold n+1 parseable components. Consuming
// exactly <len> bytes per component keeps embedded separators (e.g. the
// colons in an IPv6 host) from confusing the parse.
func keyPart(key string, n int) string {
	for i := 0; i <= n; i++ {
		c := strings.IndexByte(key, ':')
		if c < 0 {
			return ""
		}
		l, err := strconv.Atoi(key[:c])
		if err != nil || c+1+l > len(key) {
			return ""
		}
		if i == n {
			return key[c+1 : c+1+l]
		}
		key = key[c+1+l:]
	}
	return ""
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

			// Key layout is length-prefixed components:
			// <len>:METHOD<len>:scheme<len>:host<len>:path...
			keyHost := keyPart(key, 2) // method=0, scheme=1, host=2
			if keyHost == "" {
				t.Fatalf("unexpected key layout %q", key)
			}

			tag := SiteTag(host)
			wantTag := "site:" + keyHost
			if tag != wantTag {
				t.Errorf("SiteTag(%q) = %q, but key host is %q (want tag %q)",
					host, tag, keyHost, wantTag)
			}
		})
	}
}
