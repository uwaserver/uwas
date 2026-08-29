package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// browserCacheServer serves a small tree covering the cases that matter:
// a page, a hashed asset and a plain asset.
func browserCacheServer(t *testing.T, domain config.Domain) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":         "<html></html>",
		"sw.js":              "// worker",
		"style.css":          "body{}",
		"assets/app.4f2a.js": "console.log(1)",
		"assets/sw.js":       "// worker in the hashed dir",
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	domain.Root = dir
	domain.Type = "static"
	domain.SSL = config.SSLConfig{Mode: "off"}
	if domain.Host == "" {
		domain.Host = "bc.test"
	}
	if len(domain.IndexFiles) == 0 {
		domain.IndexFiles = []string{"index.html"}
	}
	// Mirror what applyDefaults fills in at load time.
	if domain.BrowserCache.HTML == "" {
		domain.BrowserCache.HTML = config.DefaultBrowserCacheHTML
	}
	if domain.BrowserCache.Immutable == "" {
		domain.BrowserCache.Immutable = config.DefaultBrowserCacheImmutable
	}

	cfg := &config.Config{
		Global:  config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{domain},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s, domain.Host
}

func getHeader(t *testing.T, s *Server, host, path, header string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = host
	s.handleRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET %s: status = %d, want 200", path, rec.Code)
	}
	return rec.Header().Get(header)
}

func TestBrowserCacheHeadersServed(t *testing.T) {
	s, host := browserCacheServer(t, config.Domain{
		BrowserCache: config.BrowserCache{ImmutablePaths: []string{"/assets/*"}},
	})

	tests := []struct {
		path string
		want string
	}{
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/style.css", ""},
		{"/assets/app.4f2a.js", config.DefaultBrowserCacheImmutable},
		{"/assets/sw.js", config.DefaultBrowserCacheImmutable},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := getHeader(t, s, host, tt.path, "Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBrowserCacheDoesNotOverrideLocation checks explicit operator intent wins:
// locations[].cache_control runs earlier in dispatch and must survive.
func TestBrowserCacheDoesNotOverrideLocation(t *testing.T) {
	s, host := browserCacheServer(t, config.Domain{
		Locations: []config.LocationConfig{{Match: "/index.html", CacheControl: "public, max-age=60"}},
	})
	if got := getHeader(t, s, host, "/index.html", "Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want the location's value", got)
	}
}

func TestBrowserCacheDisabledSendsNothing(t *testing.T) {
	s, host := browserCacheServer(t, config.Domain{
		BrowserCache: config.BrowserCache{Enabled: config.BoolPtr(false)},
	})
	if got := getHeader(t, s, host, "/index.html", "Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want empty when browser_cache is off", got)
	}
}
