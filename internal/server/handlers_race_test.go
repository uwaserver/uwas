package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// TestReloadConcurrentWithProxyRead repeatedly calls reload while
// goroutines pound on the per-domain proxy maps via handleProxy and
// the predicate-guard maps via direct snapshots. Run with -race to
// catch any future regression of the C1 fix (concurrent map read/write
// during reload). Without the handlersMu lock this test deterministically
// trips the Go runtime's "concurrent map read and map write" panic.
func TestReloadConcurrentWithProxyRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent reload stress in -short mode")
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "uwas.yaml")
	const proxyDomain = "proxy.test"

	// Two configs we will swap between on every reload so that maps
	// genuinely change identity each time, not just contents.
	cfgA := `
domains:
  - host: ` + proxyDomain + `
    type: proxy
    proxy:
      upstreams:
        - address: http://127.0.0.1:1
      algorithm: round_robin
    security:
      ip_whitelist: ["1.2.3.4/32"]
      waf:
        enabled: true
      rate_limit:
        requests: 1000
        window: 1m
    cors:
      enabled: true
      allowed_origins: ["*"]
`
	cfgB := `
domains:
  - host: ` + proxyDomain + `
    type: proxy
    proxy:
      upstreams:
        - address: http://127.0.0.1:2
      algorithm: random
    security:
      ip_whitelist: ["5.6.7.8/32"]
      waf:
        enabled: true
      rate_limit:
        requests: 2000
        window: 30s
    cors:
      enabled: true
      allowed_origins: ["https://a.example"]
`

	if err := os.WriteFile(configPath, []byte(cfgA), 0644); err != nil {
		t.Fatal(err)
	}

	log := logger.New("error", "text")
	s := New(&config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: proxyDomain, Type: "proxy", Proxy: config.ProxyConfig{
				Upstreams: []config.Upstream{{Address: "http://127.0.0.1:1"}},
			}},
		},
	}, log)
	s.SetConfigPath(configPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var reloads atomic.Int64
	var reads atomic.Int64

	// Single writer: flips between configs and reloads as fast as
	// possible. We deliberately use one writer so the config file
	// itself does not race (the file is written non-atomically by
	// os.WriteFile and would torn-read under parallel writers).
	// The race we want to expose is reload ↔ readers, not writer ↔
	// writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		useA := true
		for ctx.Err() == nil {
			content := cfgA
			if !useA {
				content = cfgB
			}
			useA = !useA
			if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
				t.Errorf("write config: %v", err)
				return
			}
			if err := s.reload(); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
			reloads.Add(1)
		}
	}()

	// Readers: snapshot the same maps handleProxy / handleRequest
	// would read on each request. We intentionally do not call into
	// the public handler chain to avoid pulling in TLS, listeners, or
	// network plumbing; the goal here is to exercise the same
	// per-domain map reads that the hot path performs.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				s.handlersMu.RLock()
				_ = s.proxyPools[proxyDomain]
				_ = s.proxyBalancers[proxyDomain]
				_ = s.proxyBreakers[proxyDomain]
				_ = s.proxyMirrors[proxyDomain]
				_ = s.proxyCanaries[proxyDomain]
				_ = s.proxyHealthChks[proxyDomain]
				_ = s.wafGuards[proxyDomain]
				_ = s.ipACLGuards[proxyDomain]
				_ = s.geoGuards[proxyDomain]
				_ = s.corsGuards[proxyDomain]
				_ = s.domainRateLimiters[proxyDomain]
				_ = s.imageOptChains[proxyDomain]
				_ = s.rewriteCache[proxyDomain]
				s.handlersMu.RUnlock()
				reads.Add(1)
			}
		}()
	}

	wg.Wait()

	if reloads.Load() == 0 {
		t.Errorf("no reloads ran in the test window")
	}
	if reads.Load() == 0 {
		t.Errorf("no reads ran in the test window")
	}
	t.Logf("completed %d reloads and %d reads", reloads.Load(), reads.Load())
}

// TestRebuildProxyPoolsConcurrentWithHandleProxy specifically targets
// the rebuildProxyPools path used by onDomainChange. It pounds on the
// handleProxy snapshot path against rebuildProxyPools running at full
// speed in another goroutine. Must be run with -race.
func TestRebuildProxyPoolsConcurrentWithHandleProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent rebuild stress in -short mode")
	}

	log := logger.New("error", "text")
	s := New(&config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}, log)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	domainsA := []config.Domain{
		{Host: "a.test", Type: "proxy", Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "http://127.0.0.1:1"}},
			Algorithm: "round_robin",
		}},
	}
	domainsB := []config.Domain{
		{Host: "a.test", Type: "proxy", Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "http://127.0.0.1:2"}},
			Algorithm: "random",
		}},
	}

	var wg sync.WaitGroup

	// Writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		useA := true
		for ctx.Err() == nil {
			if useA {
				s.rebuildProxyPools(domainsA)
			} else {
				s.rebuildProxyPools(domainsB)
			}
			useA = !useA
		}
	}()

	// Readers exercising handleProxy's snapshot.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", "/", nil)
				rctx := &router.RequestContext{
					Request:  req,
					Response: router.NewResponseWriter(rec),
				}
				domain := &config.Domain{
					Host: "a.test",
					Type: "proxy",
					Proxy: config.ProxyConfig{
						Upstreams: []config.Upstream{{Address: "http://127.0.0.1:1"}},
						Algorithm: "round_robin",
					},
				}
				// handleProxy will fail to actually proxy (the
				// upstream port is closed) but it executes the
				// snapshot reads we care about under -race before
				// any network IO.
				s.handleProxy(rctx, domain)
				// Drain status to keep the response object happy.
				_ = rctx.Response.StatusCode()
			}
		}()
	}

	wg.Wait()
}

// Ensure http.Handler import isn't tree-shaken in case of future
// refactors.
var _ http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
