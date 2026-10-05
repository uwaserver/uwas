package server

import (
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func cacheControlRemovalDomain() config.Domain {
	return config.Domain{Locations: []config.LocationConfig{{Match: "/", CacheControl: "public, max-age=600"}}, Headers: config.HeadersConfig{ResponseAdd: map[string]string{"Cache-Control": "private, max-age=5"}}, BrowserCache: config.BrowserCache{HTML: "no-cache"}}
}
func cacheControlRemovalResponse(t *testing.T, d config.Domain) (string, static.CacheControlDecision) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	d.Host = "preview.test"
	d.Root = dir
	d.Type = "static"
	d.SSL = config.SSLConfig{Mode: "off"}
	d.IndexFiles = []string{"index.html"}
	cfg := &config.Config{Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text", Cache: config.CacheConfig{Enabled: d.Cache.Enabled, MemoryLimit: config.ByteSize(4 << 20), DefaultTTL: 60}}, Domains: []config.Domain{d}}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(s.cancel)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = d.Host
	s.handleRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("invalid fixture status %d", rec.Code)
	}
	return rec.Header().Get("Cache-Control"), static.ResolveCacheControl(&d, "/index.html", s.cache != nil && d.Cache.Enabled)
}
func TestCacheControlPreviewMatchesHeaderRemoval(t *testing.T) {
	for _, c := range []struct {
		name             string
		legacy, response []string
		disable, rules   bool
		want, source     string
	}{
		{"additions control", nil, nil, false, false, "private, max-age=5", "headers"},
		{"legacy removal", []string{"CACHE-CONTROL"}, nil, false, false, "no-cache", "browser_cache"},
		{"response removal", nil, []string{"cache-control"}, false, false, "no-cache", "browser_cache"},
		{"both repeated removal", []string{"Cache-Control"}, []string{"cache-control"}, false, false, "no-cache", "browser_cache"},
		{"unrelated removal", []string{"X-Test"}, nil, false, false, "private, max-age=5", "headers"},
		{"no fallback", nil, []string{"Cache-Control"}, true, false, "", "none"},
		{"rule after removal", nil, []string{"Cache-Control"}, false, true, "public, max-age=99", "cache_rule"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := cacheControlRemovalDomain()
			d.Headers.Remove = c.legacy
			d.Headers.ResponseRemove = c.response
			if c.disable {
				d.BrowserCache.Enabled = config.BoolPtr(false)
			}
			if c.rules {
				d.Cache = config.DomainCache{Enabled: true, TTL: 60, Rules: []config.CacheRule{{Match: "index", CacheControl: c.want}}}
			}
			actual, preview := cacheControlRemovalResponse(t, d)
			if actual != c.want || preview.Value != actual || preview.Source != c.source {
				t.Fatalf("EXPECTED: value=%q source=%q ACTUAL: server=%q preview=%+v", c.want, c.source, actual, preview)
			}
		})
	}
}
