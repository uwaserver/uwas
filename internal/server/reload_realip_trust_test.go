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

// F1990: the RealIP middleware is built once, so global.trusted_proxies and the
// Cloudflare ranges stayed at their startup values across a reload: a proxy
// removed from the list kept being believed, and newly synced ranges were not.

func realIPReloadConfig(t *testing.T, dir, trusted, cfRanges string) string {
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
  trusted_proxies: [%s]
  cloudflare:
    ip_ranges: [%s]
domains:
  - host: rip.test
    type: static
    root: %s
    ssl:
      mode: "off"
    security:
      ip_blacklist: ["9.9.9.9"]
`, trusted, cfRanges, root)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func realIPStatus(h http.Handler, remote, header, value string) int {
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "rip.test"
	req.RemoteAddr = remote
	req.Header.Set("User-Agent", "uwas-test")
	if header != "" {
		req.Header.Set(header, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestReloadRefreshesRealIPTrustedProxies(t *testing.T) {
	dir := t.TempDir()
	path := realIPReloadConfig(t, dir, `"10.0.0.0/8"`, "")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	s.handler = s.buildMiddlewareChain()

	// Control: a trusted proxy's X-Real-IP is believed (blacklisted client is
	// refused); an untrusted peer's is ignored; a plain request is served.
	if got := realIPStatus(s.handler, "10.1.1.1:1", "X-Real-IP", "9.9.9.9"); got != http.StatusForbidden {
		t.Fatalf("trusted proxy forwarding blacklisted client = %d, want 403", got)
	}
	if got := realIPStatus(s.handler, "192.0.2.5:1", "X-Real-IP", "9.9.9.9"); got != http.StatusOK {
		t.Fatalf("untrusted peer's X-Real-IP honoured: %d, want 200", got)
	}
	if got := realIPStatus(s.handler, "10.1.1.1:1", "", ""); got != http.StatusOK {
		t.Fatalf("plain request = %d, want 200", got)
	}

	// The proxy is removed and a Cloudflare range arrives with the reload.
	realIPReloadConfig(t, dir, "", `"203.0.113.0/24"`)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := realIPStatus(s.handler, "10.1.1.1:1", "X-Real-IP", "9.9.9.9"); got != http.StatusOK {
		t.Errorf("removed proxy still trusted after reload: %d, want 200", got)
	}
	if got := realIPStatus(s.handler, "203.0.113.7:1", "CF-Connecting-IP", "9.9.9.9"); got != http.StatusForbidden {
		t.Errorf("new Cloudflare range not trusted after reload: %d, want 403", got)
	}

	// And back: restoring the proxy restores trust.
	realIPReloadConfig(t, dir, `"10.0.0.0/8"`, "")
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := realIPStatus(s.handler, "10.1.1.1:1", "X-Real-IP", "9.9.9.9"); got != http.StatusForbidden {
		t.Errorf("restored proxy not trusted: %d, want 403", got)
	}
}

// Requests racing a reload must be safe under -race.
func TestRealIPTrustSwapRace(t *testing.T) {
	dir := t.TempDir()
	path := realIPReloadConfig(t, dir, `"10.0.0.0/8"`, "")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	s.handler = s.buildMiddlewareChain()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				realIPStatus(s.handler, "10.1.1.1:1", "X-Real-IP", "9.9.9.9")
			}
		}
	}()
	for i := 0; i < 20; i++ {
		s.realIPTrust.Set([]string{"10.0.0.0/8"})
		s.realIPTrust.Set(nil)
	}
	close(stop)
	<-done
}
