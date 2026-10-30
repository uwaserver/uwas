package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F47700: the per-location rate limiter keyed its counters on the raw client
// address, while the domain and global limiters bucket IPv6 by /64 (an IPv6
// subscriber owns at least a /64, so one client can use any of 2^64 addresses;
// F10700). One client rotating the low 64 bits per request never met a
// location limit such as "/login 5 per minute" — the brute-force guard people
// put on a login or API path — and left one counter per address behind.
func TestLocationRateLimitBucketsIPv6ByPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "login"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host: "loc.example.com", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{{
				Match:     "/login",
				RateLimit: &config.RateLimitConfig{Requests: 2, Window: config.Duration{Duration: time.Minute}},
			}},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); time.Sleep(50 * time.Millisecond) })
	s.handler = s.buildMiddlewareChain()

	get := func(remote string) int {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.Host = "loc.example.com"
		req.RemoteAddr = remote
		req.Header.Set("User-Agent", "uwas-test")
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Rotating the low bits inside one /64: the third and later requests must
	// be refused whichever address they came from.
	t.Run("rotating addresses in one /64", func(t *testing.T) {
		var codes []int
		for _, a := range []string{"[2001:db8:1:1::1]:1", "[2001:db8:1:1::2]:1", "[2001:db8:1:1::3]:1", "[2001:db8:1:1:aaaa::4]:1", "[2001:db8:1:1:ffff:ffff:ffff:ffff]:1"} {
			codes = append(codes, get(a))
		}
		t.Logf("EXPECTED: [200 200 429 429 429]\nACTUAL: %v", codes)
		for i, want := range []int{200, 200, 429, 429, 429} {
			if codes[i] != want {
				t.Fatalf("PROBLEM CONFIRMED: request %d from a rotating /64 = %d, want %d (all codes %v)", i+1, codes[i], want, codes)
			}
		}
	})

	// Controls: a different /64 has its own bucket, a repeating /128 and an
	// IPv4 client behave as before.
	t.Run("controls", func(t *testing.T) {
		if c := get("[2001:db8:2:2::1]:1"); c != 200 {
			t.Errorf("other /64 first request = %d, want 200", c)
		}
		for i, want := range []int{200, 200, 429} {
			if c := get("203.0.113.9:1"); c != want {
				t.Errorf("IPv4 request %d = %d, want %d", i+1, c, want)
			}
		}
		// A different IPv4 address is a different client.
		if c := get("203.0.113.10:1"); c != 200 {
			t.Errorf("other IPv4 first request = %d, want 200", c)
		}
	})
}
