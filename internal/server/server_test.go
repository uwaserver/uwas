package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func testConfig(root string) *config.Config {
	return &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
		},
		Domains: []config.Domain{
			{
				Host: "localhost",
				Root: root,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
}

func TestHandleRequestStaticFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Test</h1>"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>Test</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleRequestIndexFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRequest404(t *testing.T) {
	dir := t.TempDir()

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/nonexistent", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRequestUnknownHost(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.com"
	s.handleRequest(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRequestRedirectDomain(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "old.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://new.com",
					Status:       301,
					PreservePath: true,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some/path", nil)
	req.Host = "old.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://new.com/some/path" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRequestBlockedPath(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("secret"), 0644)

	cfg := testConfig(dir)
	cfg.Domains[0].Security.BlockedPaths = []string{".git"}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.git/config", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandleRequestRewrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "target.html"), []byte("rewritten"), 0644)

	cfg := testConfig(dir)
	cfg.Domains[0].Rewrites = []config.RewriteRule{
		{Match: "^/old$", To: "/target.html"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "rewritten" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleHTTPRedirectToHTTPS(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "secure.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "auto"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page", nil)
	req.Host = "secure.com"
	s.handleHTTP(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://secure.com/page" {
		t.Errorf("Location = %q", loc)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS header should be set")
	}
}

func TestHandleProxyNoPool(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "api.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "api.com"
	s.handleRequest(rec, req)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestRenderErrorPage(t *testing.T) {
	codes := []int{400, 403, 404, 500, 502, 503, 504}
	for _, code := range codes {
		rec := httptest.NewRecorder()
		renderErrorPage(rec, code)
		if rec.Code != code {
			t.Errorf("renderErrorPage(%d) status = %d", code, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("renderErrorPage(%d) content-type = %q", code, ct)
		}
		body := rec.Body.String()
		if len(body) < 100 {
			t.Errorf("renderErrorPage(%d) body too short: %d bytes", code, len(body))
		}
	}
}

func TestServerHeaders(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if h := rec.Header().Get("Server"); h == "" {
		t.Error("Server header should be set")
	}
}
