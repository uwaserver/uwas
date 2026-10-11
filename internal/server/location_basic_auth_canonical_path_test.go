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

// F2500: location blocks (basic_auth / rate_limit) match r.URL.Path as sent,
// while the static handler cleans the path before opening the file, so a
// non-canonical path ("//private/x", "/./private/x", "/a/../private/x")
// skipped the location's basic_auth yet still served the protected file.

func locAuthServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "site")
	if err := os.MkdirAll(filepath.Join(root, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"index.html": "ok", "private/secret.html": "SECRET"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
    locations:
      - match: /private/
        basic_auth:
          enabled: true
          users: {admin: "$2a$04$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"}
`, root)
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

func locAuthGet(h http.Handler, rawPath string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = rawPath
	req.URL.RawPath = ""
	req.RequestURI = rawPath
	req.Host = "a.test"
	req.RemoteAddr = "192.0.2.5:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestLocationBasicAuthAppliesToNonCanonicalPaths(t *testing.T) {
	s := locAuthServer(t)
	if code, _ := locAuthGet(s.handler, "/private/secret.html"); code != http.StatusUnauthorized {
		t.Fatalf("/private/secret.html = %d, want 401", code)
	}
	if code, body := locAuthGet(s.handler, "/index.html"); code != http.StatusOK || body != "ok" {
		t.Fatalf("/index.html = %d %q, want 200 ok", code, body)
	}
	for _, p := range []string{"//private/secret.html", "/./private/secret.html", "/x/../private/secret.html", "/private/../private/secret.html"} {
		code, body := locAuthGet(s.handler, p)
		if code == http.StatusOK || body == "SECRET" {
			t.Errorf("%q served the protected file without credentials (%d)", p, code)
		}
	}
}
