package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestResolveCacheControlMatchesServer is the guard on the panel's
// "what would this path get?" preview.
//
// static.ResolveCacheControl reimplements the precedence that dispatch applies
// across four settings — locations, domain headers, cache rules and
// browser_cache — because the admin API cannot reach into the request path to
// ask. A second implementation is only safe if something proves the two agree,
// so this drives real requests through the real server and compares the header
// that actually comes back against what the resolver predicted.
func TestResolveCacheControlMatchesServer(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"index.html", "style.css", "app.js", "doc.pdf",
		"assets/hashed.4f2a.js", "assets/page.html", "api/data.json",
	} {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name   string
		domain config.Domain
		paths  []string
	}{
		{
			name:   "browser_cache only",
			domain: config.Domain{BrowserCache: config.BrowserCache{ImmutablePaths: []string{"/assets/*"}}},
			paths:  []string{"/index.html", "/style.css", "/assets/hashed.4f2a.js", "/assets/page.html"},
		},
		{
			name: "location overrides browser_cache",
			domain: config.Domain{
				Locations:    []config.LocationConfig{{Match: "/assets/", CacheControl: "public, max-age=600"}},
				BrowserCache: config.BrowserCache{ImmutablePaths: []string{"/assets/*"}},
			},
			paths: []string{"/assets/hashed.4f2a.js", "/assets/page.html", "/index.html"},
		},
		{
			name: "cache rule beats location",
			domain: config.Domain{
				Locations: []config.LocationConfig{{Match: "/assets/", CacheControl: "public, max-age=600"}},
				Cache: config.DomainCache{
					Enabled: true,
					TTL:     60,
					Rules:   []config.CacheRule{{Match: `\.js$`, CacheControl: "public, max-age=99"}},
				},
			},
			paths: []string{"/assets/hashed.4f2a.js", "/style.css"},
		},
		{
			name: "last matching cache rule wins",
			domain: config.Domain{
				Cache: config.DomainCache{
					Enabled: true,
					TTL:     60,
					Rules: []config.CacheRule{
						{Match: `\.js$`, CacheControl: "public, max-age=1"},
						{Match: `hashed`, CacheControl: "public, max-age=2"},
					},
				},
			},
			paths: []string{"/assets/hashed.4f2a.js", "/app.js"},
		},
		{
			name: "domain header beats location",
			domain: config.Domain{
				Locations: []config.LocationConfig{{Match: "/", CacheControl: "public, max-age=600"}},
				Headers:   config.HeadersConfig{ResponseAdd: map[string]string{"Cache-Control": "private, max-age=5"}},
			},
			paths: []string{"/index.html", "/style.css"},
		},
		{
			name: "no configuration at all",
			domain: config.Domain{
				BrowserCache: config.BrowserCache{Enabled: config.BoolPtr(false)},
			},
			paths: []string{"/index.html", "/style.css"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.domain
			d.Host = "resolve.test"
			d.Root = dir
			d.Type = "static"
			d.SSL = config.SSLConfig{Mode: "off"}
			d.IndexFiles = []string{"index.html"}
			if d.BrowserCache.HTML == "" {
				d.BrowserCache.HTML = config.DefaultBrowserCacheHTML
			}
			if d.BrowserCache.Immutable == "" {
				d.BrowserCache.Immutable = config.DefaultBrowserCacheImmutable
			}

			cfg := &config.Config{
				Global: config.GlobalConfig{
					WorkerCount: "1", LogLevel: "error", LogFormat: "text",
					Cache: config.CacheConfig{
						Enabled:     d.Cache.Enabled,
						MemoryLimit: config.ByteSize(4 << 20),
						DefaultTTL:  60,
					},
				},
				Domains: []config.Domain{d},
			}
			s := New(cfg, logger.New("error", "text"))
			defer s.cancel()

			for _, p := range tc.paths {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", p, nil)
				req.Host = d.Host
				s.handleRequest(rec, req)

				got := rec.Header().Get("Cache-Control")
				want := static.ResolveCacheControl(&d, p, s.cache != nil && d.Cache.Enabled)

				if got != want.Value {
					t.Errorf("%s: server sent %q, resolver predicted %q (source %s, detail %q)",
						p, got, want.Value, want.Source, want.Detail)
				}
			}
		})
	}
}
