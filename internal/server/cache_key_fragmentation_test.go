package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// The cache key must not let a client mint entries at will. A request's own
// Accept-Encoding spelling and an unmatched Host header both used to land in
// the key verbatim, so each distinct value stored another copy of the same
// page: every request missed (origin load) and the copies evicted the other
// tenants' hot entries (F60300, F60301).
func newFragmentationServer(t *testing.T, varyByQuery bool) (*Server, func(host, ae, path string) *httptest.ResponseRecorder) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.html"), []byte(strings.Repeat("A", 3000)), 0o644)
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{Enabled: true, MemoryLimit: config.ByteSize(64 << 20)}},
		Domains: []config.Domain{
			{Host: "frag.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60}},
		},
	}
	if !varyByQuery {
		f := false
		cfg.Global.Cache.VaryByQuery = &f
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); time.Sleep(20 * time.Millisecond) })
	s.handler = s.buildMiddlewareChain()
	return s, func(host, ae, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		req.RemoteAddr = "9.9.9.9:1"
		req.Header.Set("User-Agent", "uwas-test")
		if ae != "" {
			req.Header.Set("Accept-Encoding", ae)
		}
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		return rec
	}
}

func cacheEntries(s *Server) int64 { return s.cache.Stats()["entries"] }

func TestAcceptEncodingSpellingsShareOneCacheEntry(t *testing.T) {
	for _, varyByQuery := range []bool{true, false} {
		s, get := newFragmentationServer(t, varyByQuery)
		if rec := get("frag.test", "gzip", "/a.html"); rec.Code != 200 {
			t.Fatalf("control: status %d", rec.Code)
		}
		if rec := get("frag.test", "gzip", "/a.html"); rec.Header().Get("X-Cache") != "HIT" {
			t.Fatalf("control: identical request X-Cache=%q, want HIT", rec.Header().Get("X-Cache"))
		}
		misses := 0
		for i := 0; i < 20; i++ {
			rec := get("frag.test", fmt.Sprintf("gzip, deflate;q=0.%d, junk%d", i%9+1, i), "/a.html")
			if rec.Header().Get("X-Cache") != "HIT" {
				misses++
			}
		}
		if misses != 0 {
			t.Errorf("varyByQuery=%v: %d/20 requests differing only in Accept-Encoding spelling missed the cache", varyByQuery, misses)
		}
		if n := cacheEntries(s); n != 1 {
			t.Errorf("varyByQuery=%v: %d cache entries for one page, want 1", varyByQuery, n)
		}
	}
}

func TestAcceptEncodingClassesStaySeparate(t *testing.T) {
	_, get := newFragmentationServer(t, true)
	get("frag.test", "br", "/a.html")
	// A client that refuses gzip/br must not be answered from the br entry's
	// key, and q=0 is a refusal, not an acceptance.
	if rec := get("frag.test", "", "/a.html"); rec.Header().Get("X-Cache") == "HIT" {
		t.Errorf("identity client hit the br-class entry")
	}
	if rec := get("frag.test", "gzip;q=0, br;q=0", "/a.html"); rec.Header().Get("X-Cache") != "HIT" {
		t.Errorf("a client refusing every coding should share the identity entry, X-Cache=%q", rec.Header().Get("X-Cache"))
	}
}

func TestUnmatchedHostDoesNotMintCacheEntries(t *testing.T) {
	s, get := newFragmentationServer(t, true)
	get("frag.test", "", "/a.html")
	get("frag.test", "", "/a.html")
	before := cacheEntries(s)
	if before != 1 {
		t.Fatalf("control: configured host stored %d entries, want 1", before)
	}
	for i := 0; i < 20; i++ {
		// An unmatched Host falls through to the first domain; it still
		// answers, but must not fill the shared cache with copies.
		if rec := get(fmt.Sprintf("rand%d.attacker.example", i), "", "/a.html"); rec.Code != 200 {
			t.Fatalf("fallback domain stopped answering unmatched Host: %d", rec.Code)
		}
	}
	if after := cacheEntries(s); after != before {
		t.Errorf("unmatched Host values grew the cache from %d to %d entries", before, after)
	}
}
