package server

// F37300: a cache hit stamps its own X-Cache and Age, then copies every stored
// header over the response. A proxied upstream that sits behind another cache
// (a CDN, Varnish, another UWAS) sends its own Age and X-Cache, which are
// stored with the entry, so on a hit the stored values replaced the ones this
// server had just computed: the client saw an X-Cache that did not say HIT and
// an Age that ignored the time spent here. RFC 9111 §4.2.3: the age is the
// upstream's Age plus the time in this cache.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestCacheHitReportsItsOwnAgeAndXCache(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Age", "3000")
		w.Header().Set("X-Cache", "MISS from upstream-cdn")
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
			Host: "own.test", Type: "proxy", SSL: config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams:             []config.Upstream{{Address: backend.URL, Weight: 1}},
				AllowPrivateUpstreams: true,
			},
			Cache: config.DomainCache{Enabled: true, TTL: 600},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/a.css", nil)
		req.Host = "own.test"
		s.handleRequest(rec, req)
		return rec
	}

	// Control: the miss is a passthrough; the upstream's headers reach the client.
	miss := get()
	if miss.Code != 200 {
		t.Fatalf("control (MISS): status = %d", miss.Code)
	}

	hit := get()
	age, _ := strconv.Atoi(hit.Header().Get("Age"))
	t.Logf("EXPECTED: X-Cache=HIT, Age in [3000,3060) (upstream's 3000 plus seconds here)\nACTUAL: X-Cache=%q Age=%q",
		hit.Header().Get("X-Cache"), hit.Header().Get("Age"))
	if hit.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("PROBLEM CONFIRMED: hit reports X-Cache=%q, want HIT (the stored upstream value replaced this cache's)",
			hit.Header().Get("X-Cache"))
	}
	if age < 3000 || age >= 3060 {
		t.Fatalf("PROBLEM CONFIRMED: hit reports Age=%q, want the upstream's 3000 plus time in this cache", hit.Header().Get("Age"))
	}
	if n := len(hit.Header().Values("Age")); n != 1 {
		t.Fatalf("Age header repeated %d times", n)
	}
}

// Control: an upstream that sends no Age gets this cache's own age alone.
func TestCacheHitWithoutUpstreamAgeReportsLocalAge(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
			Host: "own.test", Type: "proxy", SSL: config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams:             []config.Upstream{{Address: backend.URL, Weight: 1}},
				AllowPrivateUpstreams: true,
			},
			Cache: config.DomainCache{Enabled: true, TTL: 600},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/b.css", nil)
		req.Host = "own.test"
		s.handleRequest(rec, req)
		if i == 1 {
			age, err := strconv.Atoi(rec.Header().Get("Age"))
			if rec.Header().Get("X-Cache") != "HIT" || err != nil || age < 0 || age >= 60 {
				t.Fatalf("hit: X-Cache=%q Age=%q", rec.Header().Get("X-Cache"), rec.Header().Get("Age"))
			}
		}
	}
}
