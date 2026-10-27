package server

// F38300: the cache guard for PHP domains looks only at the extension of the
// URL path, so /index.php/profile.css counts as a static stylesheet although
// FastCGI runs index.php for it (PATH_INFO=/profile.css). The comment on
// phpCacheableStaticExts says PHP requests are "always dynamic and bypass the
// cache"; here dynamic PHP output was stored under the URL and replayed to the
// next visitor.

import (
	"net"
	"net/http"
	"net/http/fcgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestPHPDomainDoesNotCachePathInfoRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int64
	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("run-" + strconv.FormatInt(runs.Add(1), 10)))
	}))
	t.Cleanup(func() { ln.Close() })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{Enabled: true, MemoryLimit: config.ByteSize(1 << 20)},
		},
		Domains: []config.Domain{{
			Host: "php-cache.test", Type: "php", Root: dir,
			SSL:   config.SSLConfig{Mode: "off"},
			PHP:   config.PHPConfig{FPMAddress: ln.Addr().String()},
			Cache: config.DomainCache{Enabled: true, TTL: 600},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	get := func(path string) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "php-cache.test"
		s.handleRequest(rec, req)
		return rec.Body.String()
	}

	// Control: a real static file on a PHP domain is still cacheable.
	get("/real.css")
	if h := httptest.NewRecorder(); true {
		req := httptest.NewRequest(http.MethodGet, "/real.css", nil)
		req.Host = "php-cache.test"
		s.handleRequest(h, req)
		if h.Header().Get("X-Cache") != "HIT" {
			t.Errorf("control: /real.css X-Cache = %q, want HIT", h.Header().Get("X-Cache"))
		}
	}
	// Control: the script itself is dynamic.
	a, b := get("/index.php"), get("/index.php")
	if a == b {
		t.Errorf("control: /index.php returned %q twice, want a fresh run each time", a)
	}

	for _, p := range []string{"/index.php/profile.css", "/index.php/app.js", "/invoice/123.pdf"} {
		first, second := get(p), get(p)
		t.Logf("EXPECTED: two distinct PHP runs\nACTUAL: %q then %q", first, second)
		if first == second {
			t.Errorf("PROBLEM CONFIRMED: %s served PHP output from the cache (%q twice)", p, first)
		}
	}
}
