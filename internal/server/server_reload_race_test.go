package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// TestReloadConcurrentWithRequests runs config reloads concurrently with request
// serving under -race. It locks in that reload() swaps every per-domain map
// (rewrite/route/IP-ACL/geo/CORS/WAF/rate-limit/image-opt + htaccess caches)
// under its lock rather than mutating shared maps in place — the concurrent
// map-access concern that motivated the (now superseded) PR #18. A regression
// that reintroduced an unsynchronized swap would trip the race detector here.
func TestReloadConcurrentWithRequests(t *testing.T) {
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

	var wg sync.WaitGroup

	// Reloader: repeatedly swap the per-domain maps.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := s.reload(); err != nil {
				t.Errorf("reload: %v", err)
				return
			}
		}
	}()

	// Concurrent readers hitting the per-domain map accessors via dispatch.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", "/index.html", nil)
				req.Host = "reload.test"
				s.handleRequest(rec, req)
			}
		}()
	}

	wg.Wait()
}
