package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// The per-domain access log was keyed by r.Host. The router folds case, port
// and a trailing dot onto one domain, so every spelling of the Host header
// opened another descriptor for the same file and kept it forever: a client
// could exhaust the process's descriptors one port number at a time. The
// spellings also rotated independently, so after one renamed the file the
// others kept appending to the archived inode.

func domainLogKeyServer(t *testing.T, rotate config.RotateConfig) (*Server, http.Handler, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "access.log")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host: "log.test", Type: "static", Root: root,
			SSL:       config.SSLConfig{Mode: "off"},
			AccessLog: config.AccessLogConfig{Path: logPath, Rotate: rotate},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	s.cancel()
	t.Cleanup(s.domainLogs.Close)
	return s, s.buildMiddlewareChain(), logPath
}

func domainLogKeyRequest(t *testing.T, h http.Handler, host, target string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	req.Header.Set("User-Agent", "regression-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
		t.Fatalf("host %q target %q: status %d", host, target, rec.Code)
	}
}

func TestDomainLogHostSpellingsShareOneFile(t *testing.T) {
	s, h, logPath := domainLogKeyServer(t, config.RotateConfig{})
	for _, host := range []string{"log.test", "LOG.test", "log.test:8080", "log.test.", "Log.Test:1"} {
		domainLogKeyRequest(t, h, host, "/")
	}
	for p := 10000; p < 10100; p++ {
		domainLogKeyRequest(t, h, fmt.Sprintf("log.test:%d", p), "/")
	}

	s.domainLogs.mu.RLock()
	n := len(s.domainLogs.files)
	s.domainLogs.mu.RUnlock()
	if n != 1 {
		t.Fatalf("open log entries = %d after 105 Host spellings, want 1", n)
	}
	fds := 0
	ents, _ := os.ReadDir("/proc/self/fd")
	for _, e := range ents {
		if l, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && l == logPath {
			fds++
		}
	}
	if len(ents) > 0 && fds != 1 {
		t.Fatalf("descriptors open on %s = %d, want 1", logPath, fds)
	}
}

func TestDomainLogRotationFollowedByEverySpelling(t *testing.T) {
	_, h, logPath := domainLogKeyServer(t, config.RotateConfig{MaxSize: 150})
	domainLogKeyRequest(t, h, "log.test", "/first")
	domainLogKeyRequest(t, h, "LOG.TEST", "/second") // crosses MaxSize and rotates
	domainLogKeyRequest(t, h, "log.test", "/after-rotation")

	active, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(active), "/after-rotation") {
		t.Fatalf("entry written after rotation is missing from the active log: %q", active)
	}
}

// The CLF path field is the decoded URL path, so an encoded line break must
// not split the record into a second, attacker-written line.
func TestDomainLogEncodedNewlineCannotForgeLine(t *testing.T) {
	s, h, logPath := domainLogKeyServer(t, config.RotateConfig{})
	domainLogKeyRequest(t, h, "log.test", `/x%0A203.0.113.9%20-%20-%20[x]%20%22GET%20/admin%22%20200%201%201ms`)
	domainLogKeyRequest(t, h, "log.test", "/y%0D%0Az")
	s.domainLogs.flushAll()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 || strings.Contains(string(data), "\r") {
		t.Fatalf("2 requests produced %d lines (or a raw CR): %q", got, data)
	}
}
