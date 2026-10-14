package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A cache rule's cache_control was set on the response before the handler
// ran; a proxy then copied the upstream's headers over it, so on proxied
// domains the rule never reached the client — on a miss or on a hit.
func TestCacheRuleCacheControlOverridesProxiedUpstream(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, no-cache")
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte("body{}"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{Enabled: true, MemoryLimit: config.ByteSize(1 << 20)},
		},
		Domains: []config.Domain{{
			Host: "rule.test", Type: "proxy", SSL: config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams:             []config.Upstream{{Address: backend.URL, Weight: 1}},
				AllowPrivateUpstreams: true,
			},
			Cache: config.DomainCache{
				Enabled: true, TTL: 600,
				Rules: []config.CacheRule{{Match: "^/assets/", CacheControl: "public, max-age=3600"}},
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	for _, want := range []string{"", "HIT"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assets/site.css", nil)
		req.Host = "rule.test"
		s.handleRequest(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Cache"); got != want {
			t.Fatalf("X-Cache = %q, want %q", got, want)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
			t.Errorf("X-Cache=%q: Cache-Control = %q, want the rule's value", want, got)
		}
	}
}
