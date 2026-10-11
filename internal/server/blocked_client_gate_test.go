package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2650: the autoblock ban was enforced only where a TCP connection is
// accepted (guardListener). A request reaching the handler chain any other way
// was served: HTTP/3 over UDP has no such listener, PROXY-protocol mode skips
// the guard, and behind a CDN the TCP peer is a whitelisted edge while the
// banned address only appears as the forwarded client.

func gateServer(t *testing.T, autoblock bool) *Server {
	t.Helper()
	dir := t.TempDir()
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
  trusted_proxies: ["203.0.113.0/24"]
  autoblock:
    enabled: %t
    block_duration: 1h
domains:
  - host: gate.test
    type: static
    root: %s
    ssl:
      mode: "off"
`, autoblock, root)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(p)
	s.handler = s.buildMiddlewareChain()
	return s
}

func gateStatus(h http.Handler, remote, header, value string) int {
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "gate.test"
	req.RemoteAddr = remote
	req.Header.Set("User-Agent", "uwas-test")
	if header != "" {
		req.Header.Set(header, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestBlockedClientRefusedOnEveryEntryPath(t *testing.T) {
	s := gateServer(t, true)

	// Control: nothing is banned yet.
	if got := gateStatus(s.handler, "198.51.100.7:4000", "", ""); got != http.StatusOK {
		t.Fatalf("unbanned direct request = %d, want 200", got)
	}
	if err := s.autoblocker.Block("198.51.100.7", "waf", time.Hour); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct{ remote, header, value string }{
		"direct peer (HTTP/3, PROXY protocol)":  {"198.51.100.7:4000", "", ""},
		"IPv4-mapped peer":                      {"[::ffff:198.51.100.7]:4000", "", ""},
		"forwarded by a trusted proxy (CDN)":    {"203.0.113.9:443", "CF-Connecting-IP", "198.51.100.7"},
		"forwarded via X-Real-IP trusted proxy": {"203.0.113.9:443", "X-Real-IP", "198.51.100.7"},
	} {
		if got := gateStatus(s.handler, c.remote, c.header, c.value); got != http.StatusForbidden {
			t.Errorf("%s: banned client = %d, want 403", name, got)
		}
	}

	// Controls: other clients, the trusted proxy itself and an untrusted peer
	// claiming a banned address in a header are all unaffected.
	if got := gateStatus(s.handler, "198.51.100.8:4000", "", ""); got != http.StatusOK {
		t.Errorf("unrelated client = %d, want 200", got)
	}
	if got := gateStatus(s.handler, "203.0.113.9:443", "", ""); got != http.StatusOK {
		t.Errorf("trusted proxy without a forwarded client = %d, want 200", got)
	}
	if got := gateStatus(s.handler, "192.0.2.50:1", "CF-Connecting-IP", "198.51.100.7"); got != http.StatusOK {
		t.Errorf("untrusted peer's forged header = %d, want 200", got)
	}

	// Lifting the ban restores service.
	if err := s.autoblocker.Unblock("198.51.100.7"); err != nil {
		t.Fatal(err)
	}
	if got := gateStatus(s.handler, "198.51.100.7:4000", "", ""); got != http.StatusOK {
		t.Errorf("unbanned again = %d, want 200", got)
	}
}

func TestBlockedClientGateInertWhenAutoblockOff(t *testing.T) {
	s := gateServer(t, false)
	if s.autoblocker.Enabled() {
		t.Fatal("autoblock should be off")
	}
	if got := gateStatus(s.handler, "198.51.100.7:4000", "", ""); got != http.StatusOK {
		t.Fatalf("autoblock off: %d, want 200", got)
	}
}

// The gate reads the ban on every request, so it is safe while bans change.
func TestBlockedClientGateRacesWithBlockAndUnblock(t *testing.T) {
	s := gateServer(t, true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = s.autoblocker.Block("198.51.100.7", "waf", time.Hour)
			_ = s.autoblocker.Unblock("198.51.100.7")
		}
	}()
	for i := 0; i < 200; i++ {
		got := gateStatus(s.handler, "198.51.100.7:4000", "", "")
		if got != http.StatusOK && got != http.StatusForbidden {
			t.Fatalf("status %d while bans flip", got)
		}
	}
	<-done
}
