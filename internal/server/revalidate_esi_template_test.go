package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F39300: the inline store path marks an ESI page as a template so every hit
// assembles its <esi:include> tags; the background refresh stored the same
// response as a plain body. After the first refresh every visitor received
// the raw markup instead of the assembled page.
func TestRevalidateKeepsESITemplate(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/frag" {
			w.Write([]byte("FRAGMENT-OK"))
			return
		}
		w.Write([]byte(`<html><body>A<!--esi <esi:include src="/frag"/> -->B</body></html>`))
	}))
	t.Cleanup(origin.Close)

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{Enabled: true, MemoryLimit: config.ByteSize(8 << 20),
				DefaultTTL: 1, GraceTTL: 60, StaleWhileRevalidate: true}},
		Domains: []config.Domain{{
			Host: "esi.test", Type: "proxy", Root: t.TempDir(), SSL: config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 1, ESI: true},
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: origin.URL, Weight: 1}}},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	get := func() (string, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "esi.test"
		req.RemoteAddr = "203.0.113.9:4000"
		req.Header.Set("User-Agent", "uwas-test")
		s.handleRequest(rec, req)
		return rec.Header().Get("X-Cache"), rec.Body.String()
	}
	get() // fill
	if st, b := get(); st != cache.StatusHit || !strings.Contains(b, "FRAGMENT-OK") || strings.Contains(b, "esi") {
		t.Fatalf("control premise broken: hit before refresh = %q (X-Cache=%q)", b, st)
	}
	time.Sleep(1100 * time.Millisecond) // past TTL, inside grace
	if st, _ := get(); st != cache.StatusStale {
		t.Fatalf("X-Cache = %q, want STALE", st)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && s.revalActive.Load() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	st, b := get()
	t.Logf("EXPECTED: assembled page containing FRAGMENT-OK\nACTUAL: X-Cache=%q body=%q", st, b)
	if strings.Contains(b, "esi") || !strings.Contains(b, "FRAGMENT-OK") {
		t.Fatalf("PROBLEM CONFIRMED: after a background refresh visitors get the raw ESI markup")
	}
}

// The same divergence: the inline path adds a content ETag to a dynamic
// response that has none, so clients can revalidate. A refreshed entry lost it.
func TestRevalidateKeepsGeneratedETag(t *testing.T) {
	s, _, _ := staleServer(t, 1, 60, true)
	etagOf := func() (string, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "stale.test"
		s.handleRequest(rec, req)
		return rec.Header().Get("X-Cache"), rec.Header().Get("Etag")
	}
	etagOf() // fill
	if st, e := etagOf(); st != cache.StatusHit || e == "" {
		t.Fatalf("control premise broken: hit before refresh X-Cache=%q ETag=%q", st, e)
	}
	time.Sleep(1100 * time.Millisecond) // past TTL, inside grace
	if st, _ := etagOf(); st != cache.StatusStale {
		t.Fatalf("X-Cache = %q, want STALE", st)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && s.revalActive.Load() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	st, e := etagOf()
	t.Logf("EXPECTED: ETag present after refresh\nACTUAL: X-Cache=%q ETag=%q", st, e)
	if e == "" {
		t.Fatalf("PROBLEM CONFIRMED: a refreshed entry has no ETag")
	}
}
