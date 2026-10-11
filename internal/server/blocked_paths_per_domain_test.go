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

// F2140: the global SecurityGuard used to be built once from the union of every
// domain's security.blocked_paths, so one tenant's list returned 403 for the
// same path on every other domain, and an entry removed by a reload stayed
// blocked until restart. Each domain's list is now enforced per request.

func blockedPathsConfig(t *testing.T, dir, aBlocked string, aLocation bool) string {
	t.Helper()
	for _, n := range []string{"a", "b", "loc"} {
		root := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Join(root, "invoices"), 0o755); err != nil {
			t.Fatal(err)
		}
		for rel, body := range map[string]string{
			"index.html":           "ok",
			"public.html":          "ok",
			"invoices/report.html": "ok",
		} {
			if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	loc := ""
	if aLocation {
		loc = fmt.Sprintf(`
    locations:
      - match: /invoices/
        root: %s`, filepath.Join(dir, "loc"))
	}
	y := fmt.Sprintf(`global:
  worker_count: "1"
  log_level: error
  log_format: text
domains:
  - host: a.test
    type: static
    root: %s
    ssl: {mode: "off"}
    security:
      blocked_paths: [%s]%s
  - host: b.test
    type: static
    root: %s
    ssl: {mode: "off"}
`, filepath.Join(dir, "a"), aBlocked, loc, filepath.Join(dir, "b"))
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func blockedStatus(h http.Handler, host, path string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	req.RemoteAddr = "192.0.2.5:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func newBlockedPathsServer(t *testing.T, path string) *Server {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	s.handler = s.buildMiddlewareChain()
	return s
}

func TestBlockedPathsArePerDomain(t *testing.T) {
	dir := t.TempDir()
	s := newBlockedPathsServer(t, blockedPathsConfig(t, dir, `"invoices"`, false))

	// Controls: the owning domain still blocks its path and serves the rest;
	// the built-in list still applies everywhere.
	if got := blockedStatus(s.handler, "a.test", "/invoices/report.html"); got != http.StatusForbidden {
		t.Errorf("a.test blocked path = %d, want 403", got)
	}
	if got := blockedStatus(s.handler, "a.test", "/public.html"); got != http.StatusOK {
		t.Errorf("a.test public = %d, want 200", got)
	}
	if got := blockedStatus(s.handler, "b.test", "/.git/config"); got != http.StatusForbidden {
		t.Errorf("b.test built-in blocked path = %d, want 403", got)
	}

	// The defect: another domain is not affected by a.test's list.
	if got := blockedStatus(s.handler, "b.test", "/invoices/report.html"); got != http.StatusOK {
		t.Errorf("b.test /invoices/report.html = %d, want 200 (b.test blocks nothing)", got)
	}
}

func TestBlockedPathsFollowReload(t *testing.T) {
	dir := t.TempDir()
	path := blockedPathsConfig(t, dir, `"invoices"`, false)
	s := newBlockedPathsServer(t, path)
	if got := blockedStatus(s.handler, "a.test", "/invoices/report.html"); got != http.StatusForbidden {
		t.Fatalf("before reload = %d, want 403", got)
	}

	blockedPathsConfig(t, dir, "", false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := blockedStatus(s.handler, "a.test", "/invoices/report.html"); got != http.StatusOK {
		t.Errorf("removed entry still blocked after reload: %d, want 200", got)
	}

	// And a newly added entry takes effect without a restart.
	blockedPathsConfig(t, dir, `"public"`, false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := blockedStatus(s.handler, "a.test", "/public.html"); got != http.StatusForbidden {
		t.Errorf("new entry not applied after reload: %d, want 403", got)
	}
	if got := blockedStatus(s.handler, "b.test", "/public.html"); got != http.StatusOK {
		t.Errorf("b.test /public.html = %d, want 200", got)
	}
}

// A blocked path must also hold for requests a location handler would serve:
// the check runs before the location overrides, as the global guard did.
func TestBlockedPathsApplyBeforeLocationHandlers(t *testing.T) {
	dir := t.TempDir()
	s := newBlockedPathsServer(t, blockedPathsConfig(t, dir, `"invoices"`, true))
	if got := blockedStatus(s.handler, "a.test", "/invoices/report.html"); got != http.StatusForbidden {
		t.Errorf("location-served blocked path = %d, want 403", got)
	}
}
