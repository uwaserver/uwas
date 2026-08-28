package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// global.cache.vary_by_query reaching the engine is the half no test would
// otherwise cover: QueryVaries can be right while nothing calls
// SetVaryByQuery, which is the shape of the bug being fixed.

func varyFixture(t *testing.T, varyByQuery *bool) (*Server, http.Handler) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>sayfa"), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(64 << 20),
				DefaultTTL:  300,
				VaryByQuery: varyByQuery,
			},
		},
		Domains: []config.Domain{{
			Host:  "vary.test",
			Type:  "static",
			Root:  root,
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 300},
		}},
	}

	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s, s.buildMiddlewareChain()
}

func varyRequest(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "vary.test"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertCached proves the first request actually populated the cache, so the
// second assertion cannot pass vacuously.
func assertCached(t *testing.T, s *Server, path string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "vary.test"
	if entry, _ := s.cache.Get(req); entry == nil {
		t.Fatalf("%s was not cached — the test must not pass vacuously", path)
	}
}

func TestServerVaryByQueryDefaultsToOn(t *testing.T) {
	s, h := varyFixture(t, nil) // unset

	if rec := varyRequest(h, "/index.html?a=1"); rec.Code != http.StatusOK {
		t.Fatalf("durum %d", rec.Code)
	}
	assertCached(t, s, "/index.html?a=1")

	req := httptest.NewRequest(http.MethodGet, "/index.html?a=2", nil)
	req.Host = "vary.test"
	if entry, _ := s.cache.Get(req); entry != nil {
		t.Error("an unset vary_by_query collapsed the queries — ?a=1 and ?a=2 share one entry")
	}
}

func TestServerVaryByQueryDisabledCollapses(t *testing.T) {
	s, h := varyFixture(t, config.BoolPtr(false))

	if rec := varyRequest(h, "/index.html?utm_source=a"); rec.Code != http.StatusOK {
		t.Fatalf("durum %d", rec.Code)
	}
	assertCached(t, s, "/index.html?utm_source=a")

	req := httptest.NewRequest(http.MethodGet, "/index.html?utm_source=b", nil)
	req.Host = "vary.test"
	if entry, _ := s.cache.Get(req); entry == nil {
		t.Error("vary_by_query: false does not reach the engine — the queries stayed in separate entries")
	}
}

func TestServerVaryByQueryExplicitTrue(t *testing.T) {
	s, h := varyFixture(t, config.BoolPtr(true))

	if rec := varyRequest(h, "/index.html?a=1"); rec.Code != http.StatusOK {
		t.Fatalf("durum %d", rec.Code)
	}
	assertCached(t, s, "/index.html?a=1")

	req := httptest.NewRequest(http.MethodGet, "/index.html?a=2", nil)
	req.Host = "vary.test"
	if entry, _ := s.cache.Get(req); entry != nil {
		t.Error("an explicit true collapsed the queries")
	}
}
