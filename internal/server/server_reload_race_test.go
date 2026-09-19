package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// TestReloadConcurrentWithRequests runs config reloads concurrently with request
// serving under -race. It verifies that reload() correctly uses the atomic
// chain-swap pattern: every per-domain map (rewrite/route/IP-ACL/geo/CORS/WAF/
// rate-limit/image-opt) is rebuilt and then atomically swapped in a single
// routeMu.Lock() window. Concurrent readers either see the old snapshot or the
// new snapshot — never a partially-built intermediate state.
//
// Without the atomic swap, a reader could observe the old proxyPools map but
// the new domainChains map (different lengths → index out of range panic), or
// read a map while it is being written (concurrent map read/write fatal error).
//
// A regression that reintroduces unsynchronized in-place mutation of any shared
// map would trip the Go race detector here — provided the test is run with:
//
//	go test -race ./internal/server/...
//
// (requires gcc or clang to compile the race detector; on Debian/Ubuntu:
//	sudo apt-get install -y gcc
// )
func TestReloadConcurrentWithRequests(t *testing.T) {
	const (
		httpLoops   = 500
		reloadLoops = 200
		httpN       = 4 // concurrent request goroutines
		reloadM     = 2 // concurrent reload goroutines
	)

	webroot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webroot, "index.html"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "uwas.yaml")
	// A domain exercising every per-domain map rebuilt during reload.
	cfgYAML := `
global:
  worker_count: "1"
  log_level: error
  log_format: text
domains:
  - host: reload.test
    type: static
    root: ` + webroot + `
    ssl:
      mode: "off"
    rewrites:
      - match: "^/old$"
        to: "/index.html"
        status: 302
    security:
      geo_block_countries: ["CN"]
      ip_blacklist: ["10.0.0.1"]
      waf:
        enabled: true
    cors:
      enabled: true
      allowed_origins: ["https://x.example"]
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatal(err)
	}

	s := newDispatchTestServer(t, []config.Domain{{Host: "reload.test", Type: "static", Root: webroot}})
	s.configPath = cfgPath
	if err := s.reload(); err != nil {
		t.Fatalf("initial reload: %v", err)
	}

	var (
		wg         sync.WaitGroup
		reloadErrs int64 // atomic counter for reload errors
		httpServed int64 // atomic counter for served requests
	)

	// --- HTTP path: exercise the full s.handler.ServeHTTP stack ---
	httpServer := httptest.NewServer(s.handler)
	defer httpServer.Close()

	// --- HTTPS path: same handler through a TLS server (auto-generates cert) ---
	httpsServer := httptest.NewTLSServer(s.handler)
	defer httpsServer.Close()

	// Reloader goroutines: repeatedly trigger atomic chain swap.
	for m := 0; m < reloadM; m++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < reloadLoops; i++ {
				if err := s.reload(); err != nil {
					atomic.AddInt64(&reloadErrs, 1)
				}
				// runtime.Gosched() yields the processor to encourage preemption,
				// making the race detector more effective at finding races that
				// depend on interleaving at specific instruction boundaries.
				runtime.Gosched()
			}
		}()
	}

	// HTTP reader goroutines: exercise the ServeHTTP path.
	for n := 0; n < httpN; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := httpServer.Client()
			for i := 0; i < httpLoops; i++ {
				req, _ := http.NewRequest("GET", httpServer.URL+"/index.html", nil)
				req.Host = "reload.test"
				if resp, err := client.Do(req); err == nil {
					resp.Body.Close()
					atomic.AddInt64(&httpServed, 1)
				}
				runtime.Gosched()
			}
		}()
	}

	// HTTPS reader goroutines: same, over TLS.
	for n := 0; n < httpN; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := httpsServer.Client()
			for i := 0; i < httpLoops; i++ {
				req, _ := http.NewRequest("GET", httpsServer.URL+"/index.html", nil)
				req.Host = "reload.test"
				if resp, err := client.Do(req); err == nil {
					resp.Body.Close()
					atomic.AddInt64(&httpServed, 1)
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	// Assert no reload errors occurred during the stress test.
	if n := atomic.LoadInt64(&reloadErrs); n > 0 {
		t.Errorf("%d reload errors during stress test (should be 0)", n)
	}
	if n := atomic.LoadInt64(&httpServed); n == 0 {
		t.Error("no HTTP requests were served — handler may be broken")
	}
	t.Logf("served %d requests across %d reader goroutines with %d reloads by %d reloader goroutines",
		atomic.LoadInt64(&httpServed), httpN*2, reloadLoops*reloadM, reloadM)
}

// TestReloadConcurrentWithRequestsHTTP exercises the same scenario but routes
// exclusively through the plain HTTP path, for use in environments where the
// full TLS test cannot be run.
func TestReloadConcurrentWithRequestsHTTP(t *testing.T) {
	const (
		httpLoops   = 500
		reloadLoops = 200
		httpN       = 8 // more goroutines to increase contention
		reloadM     = 2
	)

	webroot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webroot, "index.html"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "uwas.yaml")
	cfgYAML := `
global:
  worker_count: "1"
  log_level: error
  log_format: text
domains:
  - host: reload.test
    type: static
    root: ` + webroot + `
    ssl:
      mode: "off"
`
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatal(err)
	}

	s := newDispatchTestServer(t, []config.Domain{{Host: "reload.test", Type: "static", Root: webroot}})
	s.configPath = cfgPath
	if err := s.reload(); err != nil {
		t.Fatalf("initial reload: %v", err)
	}

	httpServer := httptest.NewServer(s.handler)
	defer httpServer.Close()

	var (
		wg         sync.WaitGroup
		reloadErrs int64
		httpServed int64
	)

	for m := 0; m < reloadM; m++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < reloadLoops; i++ {
				if err := s.reload(); err != nil {
					atomic.AddInt64(&reloadErrs, 1)
				}
				runtime.Gosched()
			}
		}()
	}

	for n := 0; n < httpN; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := httpServer.Client()
			for i := 0; i < httpLoops; i++ {
				req, _ := http.NewRequest("GET", httpServer.URL+"/index.html", nil)
				req.Host = "reload.test"
				if resp, err := client.Do(req); err == nil {
					resp.Body.Close()
					atomic.AddInt64(&httpServed, 1)
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	if n := atomic.LoadInt64(&reloadErrs); n > 0 {
		t.Errorf("%d reload errors during stress test", n)
	}
	if atomic.LoadInt64(&httpServed) == 0 {
		t.Error("no requests were served")
	}
}
