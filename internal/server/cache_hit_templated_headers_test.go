package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F39302: a response header template such as `X-Client-IP: $remote_addr` is
// resolved per request, but the value stored with a cached entry replaced it
// on every hit, so each later visitor was shown the address of whoever filled
// the cache.
func TestCacheHitResolvesHeaderTemplatesPerRequest(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "shared body")
	}))
	t.Cleanup(origin.Close)

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{Enabled: true, MemoryLimit: config.ByteSize(8 << 20), DefaultTTL: 300}},
		Domains: []config.Domain{{
			Host: "hdr.test", Type: "proxy", Root: t.TempDir(), SSL: config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 300},
			Headers: config.HeadersConfig{
				ResponseAdd: map[string]string{"X-Client-IP": "$remote_addr", "X-Site": "static-value"},
			},
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: origin.URL, Weight: 1}}},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	get := func(remote string) (string, string, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/page", nil)
		req.Host = "hdr.test"
		req.RemoteAddr = remote + ":4000"
		req.Header.Set("User-Agent", "uwas-test")
		s.handleRequest(rec, req)
		return rec.Header().Get("X-Cache"), rec.Header().Get("X-Client-IP"), rec.Header().Get("X-Site")
	}
	if _, ip, _ := get("203.0.113.9"); ip != "203.0.113.9" {
		t.Fatalf("control premise broken: filling request saw X-Client-IP=%q", ip)
	}
	st, ip, site := get("198.51.100.7")
	t.Logf("EXPECTED: X-Cache=HIT X-Client-IP=198.51.100.7 X-Site=static-value\nACTUAL: X-Cache=%q X-Client-IP=%q X-Site=%q", st, ip, site)
	if st != cache.StatusHit {
		t.Fatalf("control premise broken: second request was %q, want HIT", st)
	}
	if site != "static-value" {
		t.Errorf("static header lost on hit: %q", site)
	}
	if ip != "198.51.100.7" {
		t.Fatalf("PROBLEM CONFIRMED: a cache hit shows the previous visitor's address %q", ip)
	}
}
