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

// F2530-F2532: rules that decide on r.URL.Path as sent (.htaccess RewriteRule
// patterns, cache bypass rules / WordPress bypass prefixes) are skipped by a
// non-canonical path ("//private/x") that the static handler / upstream still
// resolves to the same resource.

func nonCanonServer(t *testing.T, htaccess, extraYAML string) *Server {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "site")
	if err := os.MkdirAll(filepath.Join(root, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"index.html": "ok", "private/secret.html": "SECRET", "nocache.html": "DYN"}
	if htaccess != "" {
		files[".htaccess"] = htaccess
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	y := fmt.Sprintf(`global:
  worker_count: "1"
  log_level: error
  log_format: text
  cache: {enabled: true, memory_limit: 64MB, disk_path: "%s/cache"}
domains:
  - host: a.test
    type: static
    root: %s
    ssl: {mode: "off"}
%s`, dir, root, extraYAML)
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

func nonCanonGet(h http.Handler, rawPath string) (int, string, string) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = rawPath
	req.URL.RawPath = ""
	req.RequestURI = rawPath
	req.Host = "a.test"
	req.RemoteAddr = "192.0.2.5:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header().Get("X-Cache")
}

// An extra slash or dot segment must not hide a path from a rule that guards
// it: .htaccess RewriteRule [F] (F2530), cache bypass rules and the WordPress
// bypass prefixes (F2531), and domain rewrites [F] (F2532).
func TestHtaccessRewriteRuleAppliesToNonCanonicalPaths(t *testing.T) {
	s := nonCanonServer(t, "RewriteEngine On\nRewriteRule ^private/ - [F]\n", "    htaccess: {mode: import}\n")
	// control: canonical path is forbidden, unrelated path is served
	if code, _, _ := nonCanonGet(s.handler, "/private/secret.html"); code != http.StatusForbidden {
		t.Fatalf("control: /private/secret.html = %d, want 403", code)
	}
	if code, body, _ := nonCanonGet(s.handler, "/index.html"); code != http.StatusOK || body != "ok" {
		t.Fatalf("control: /index.html = %d %q", code, body)
	}
	bad := 0
	for _, p := range []string{"//private/secret.html", "/./private/secret.html", "/x/../private/secret.html"} {
		_, body, _ := nonCanonGet(s.handler, p)
		if body == "SECRET" {
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d non-canonical paths served the file a RewriteRule [F] forbids", bad)
	}
}

func TestCacheBypassRuleAppliesToNonCanonicalPaths(t *testing.T) {
	s := nonCanonServer(t, "", `    cache:
      enabled: true
      ttl: 60
      rules:
        - match: ^/nocache
          bypass: true
`)
	// positive control: an ordinary path is cached (HIT on the second request)
	nonCanonGet(s.handler, "/index.html")
	if _, _, xc := nonCanonGet(s.handler, "/index.html"); xc != "HIT" {
		t.Fatalf("control: /index.html not cached (X-Cache=%q)", xc)
	}
	// control: canonical path matches the bypass rule, never a cache hit
	nonCanonGet(s.handler, "/nocache.html")
	if _, _, xc := nonCanonGet(s.handler, "/nocache.html"); xc == "HIT" {
		t.Fatalf("control: bypassed path hit the cache")
	}
	nonCanonGet(s.handler, "//nocache.html")
	_, _, xc := nonCanonGet(s.handler, "//nocache.html")
	if xc == "HIT" {
		t.Fatalf("//nocache.html was cached despite bypass rule ^/nocache")
	}
}

func TestDomainRewriteRuleAppliesToNonCanonicalPaths(t *testing.T) {
	s := nonCanonServer(t, "", `    rewrites:
      - match: ^/private/
        to: "-"
        flags: [F]
`)
	if code, _, _ := nonCanonGet(s.handler, "/private/secret.html"); code != http.StatusForbidden {
		t.Fatalf("control: /private/secret.html = %d, want 403", code)
	}
	if code, body, _ := nonCanonGet(s.handler, "/index.html"); code != http.StatusOK || body != "ok" {
		t.Fatalf("control: /index.html = %d %q", code, body)
	}
	bad := 0
	for _, p := range []string{"//private/secret.html", "/./private/secret.html", "/x/../private/secret.html"} {
		_, body, _ := nonCanonGet(s.handler, p)
		if body == "SECRET" {
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d non-canonical paths served the file a rewrite [F] rule forbids", bad)
	}
}

// The WordPress bypass prefixes are canonicalised too: a proxied WordPress
// admin page requested as "//wp-admin/x" must not be stored.
func TestWordPressCacheBypassNonCanonical(t *testing.T) {
	s := nonCanonServer(t, "", `    cache:
      enabled: true
      ttl: 60
`)
	root := s.config.Domains[0].Root
	if err := os.MkdirAll(filepath.Join(root, "wp-admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "wp-admin", "x.html"), []byte("ADMIN"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/wp-admin/x.html", "//wp-admin/x.html", "/a/../wp-admin/x.html"} {
		nonCanonGet(s.handler, p)
		if _, _, xc := nonCanonGet(s.handler, p); xc == "HIT" {
			t.Errorf("%q was served from the cache", p)
		}
	}
}

// F2533: security.blocked_paths entries match the canonical path too.
func TestDomainBlockedPathsApplyToNonCanonicalPaths(t *testing.T) {
	s := nonCanonServer(t, "", `    security:
      blocked_paths: ["/private/secret.html"]
`)
	if code, _, _ := nonCanonGet(s.handler, "/private/secret.html"); code != http.StatusForbidden {
		t.Fatalf("control: /private/secret.html = %d, want 403", code)
	}
	if code, body, _ := nonCanonGet(s.handler, "/index.html"); code != http.StatusOK || body != "ok" {
		t.Fatalf("control: /index.html = %d %q", code, body)
	}
	for _, p := range []string{"/private//secret.html", "/private/./secret.html", "/private/x/../secret.html"} {
		if _, body, _ := nonCanonGet(s.handler, p); body == "SECRET" {
			t.Errorf("%q served a file security.blocked_paths names", p)
		}
	}
}
