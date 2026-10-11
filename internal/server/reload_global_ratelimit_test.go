package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F1991: global.rate_limit was captured when the chain was built, so a reload
// that enabled, changed or disabled it left the startup limiter in place.

func globalRLConfig(t *testing.T, dir string, requests int) string {
	t.Helper()
	root := filepath.Join(dir, "site")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	y := fmt.Sprintf(`global:
  worker_count: "1"
  log_level: error
  log_format: text
  rate_limit:
    requests: %d
    window: 1h
domains:
  - host: rl.test
    type: static
    root: %s
    ssl:
      mode: "off"
`, requests, root)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// globalRLBurst sends n requests from one client and counts served vs limited.
func globalRLBurst(h http.Handler, client string, n int) (served, limited int) {
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
		req.Host = "rl.test"
		req.RemoteAddr = client + ":1111"
		req.Header.Set("User-Agent", "uwas-test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited++
		} else {
			served++
		}
	}
	return
}

func TestReloadAppliesGlobalRateLimit(t *testing.T) {
	dir := t.TempDir()
	path := globalRLConfig(t, dir, 0)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	s.handler = s.buildMiddlewareChain()

	// Control: off at startup means unlimited.
	if served, limited := globalRLBurst(s.handler, "198.51.100.1", 6); served != 6 || limited != 0 {
		t.Fatalf("limit off: served=%d limited=%d, want 6/0", served, limited)
	}

	// Enabled by reload: 2 per window per client.
	globalRLConfig(t, dir, 2)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if served, limited := globalRLBurst(s.handler, "198.51.100.2", 6); served != 2 || limited != 4 {
		t.Errorf("after enabling: served=%d limited=%d, want 2/4", served, limited)
	}

	// Raised by reload: 4 per window (a fresh limiter, so a fresh budget).
	globalRLConfig(t, dir, 4)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if served, limited := globalRLBurst(s.handler, "198.51.100.3", 6); served != 4 || limited != 2 {
		t.Errorf("after raising: served=%d limited=%d, want 4/2", served, limited)
	}

	// Disabled by reload: unlimited again.
	globalRLConfig(t, dir, 0)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if served, limited := globalRLBurst(s.handler, "198.51.100.4", 6); served != 6 || limited != 0 {
		t.Errorf("after disabling: served=%d limited=%d, want 6/0", served, limited)
	}
}

// A limit configured at startup still applies, and requests racing a swap are
// safe under -race.
func TestGlobalRateLimitStartupAndSwapRace(t *testing.T) {
	dir := t.TempDir()
	path := globalRLConfig(t, dir, 3)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	s.handler = s.buildMiddlewareChain()

	if served, limited := globalRLBurst(s.handler, "198.51.100.5", 5); served != 3 || limited != 2 {
		t.Fatalf("startup limit: served=%d limited=%d, want 3/2", served, limited)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				globalRLBurst(s.handler, "198.51.100.6", 1)
			}
		}
	}()
	for i := 0; i < 20; i++ {
		s.globalRL.set(1, 0)
		s.globalRL.set(0, 0)
	}
	close(stop)
	<-done
}
