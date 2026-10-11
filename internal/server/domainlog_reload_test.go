package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2200/F2201: a per-domain access log stays open across reloads, so its
// rotation limits must follow the live config and a removed domain's file
// must be released.

func domainLogRotated(t *testing.T, base string) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Dir(base))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if e.Name() != filepath.Base(base) && strings.HasPrefix(e.Name(), filepath.Base(base)) {
			n++
		}
	}
	return n
}

func domainLogLine(m *domainLogManager, path string, max config.ByteSize) {
	m.Write("h", config.AccessLogConfig{Path: path, Rotate: config.RotateConfig{MaxSize: max}},
		"GET", "/", "1.2.3.4", "ua", 200, 1, time.Millisecond)
}

func TestDomainLogRotateLimitFollowsConfig(t *testing.T) {
	// Control: a fresh log with a tiny limit rotates.
	m1 := newDomainLogManager()
	p1 := filepath.Join(t.TempDir(), "c.log")
	domainLogLine(m1, p1, 1)
	m1.Close()
	if domainLogRotated(t, p1) < 1 {
		t.Fatal("control: tiny max_size did not rotate")
	}

	// Lowering the limit after the file is open takes effect.
	m2 := newDomainLogManager()
	p2 := filepath.Join(t.TempDir(), "lower.log")
	domainLogLine(m2, p2, 1<<20)
	domainLogLine(m2, p2, 1)
	m2.Close()
	if got := domainLogRotated(t, p2); got < 1 {
		t.Fatalf("lowered max_size ignored: %d rotated files", got)
	}

	// Raising it stops rotation again.
	m3 := newDomainLogManager()
	p3 := filepath.Join(t.TempDir(), "raise.log")
	domainLogLine(m3, p3, 1<<20)
	domainLogLine(m3, p3, 1<<20)
	domainLogLine(m3, p3, 1<<20)
	m3.Close()
	if got := domainLogRotated(t, p3); got != 0 {
		t.Fatalf("large max_size rotated anyway: %d files", got)
	}
}

func TestDomainLogRetainReleasesOnlyDroppedPaths(t *testing.T) {
	m := newDomainLogManager()
	dir := t.TempDir()
	keep, drop := filepath.Join(dir, "keep.log"), filepath.Join(dir, "drop.log")
	domainLogLine(m, keep, 0)
	domainLogLine(m, drop, 0)
	m.mu.RLock()
	droppedFile := m.files[drop].f
	keptFile := m.files[keep].f
	m.mu.RUnlock()

	m.retain(map[string]struct{}{keep: {}})

	m.mu.RLock()
	_, hasKeep := m.files[keep]
	_, hasDrop := m.files[drop]
	m.mu.RUnlock()
	if !hasKeep || hasDrop {
		t.Fatalf("entries after retain: keep=%v drop=%v", hasKeep, hasDrop)
	}
	if _, err := droppedFile.Stat(); err == nil {
		t.Fatal("dropped log descriptor still open")
	}
	if _, err := keptFile.Stat(); err != nil {
		t.Fatalf("kept log descriptor closed: %v", err)
	}

	// Empty keep set releases everything; a later write reopens the file.
	m.retain(nil)
	domainLogLine(m, drop, 0)
	m.Close()
	data, err := os.ReadFile(drop)
	if err != nil || strings.Count(string(data), "\n") != 2 {
		t.Fatalf("reopened log = %q, %v; want both lines", data, err)
	}
}

// Writers racing the release must neither panic nor trip the race detector;
// the barrier releases them all at once so retain lands mid-stream.
func TestDomainLogRetainRacesWriters(t *testing.T) {
	m := newDomainLogManager()
	path := filepath.Join(t.TempDir(), "race.log")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				domainLogLine(m, path, 1<<20)
			}
		}()
	}
	close(start)
	for i := 0; i < 20; i++ {
		m.retain(nil)
	}
	wg.Wait()
	m.Close()
}

func TestReloadReleasesRemovedDomainAccessLog(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "site")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "gone.log")
	write := func(withLogged bool) string {
		y := fmt.Sprintf("global:\n  worker_count: \"1\"\n  log_level: error\n  log_format: text\ndomains:\n  - host: keep.test\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n", root)
		if withLogged {
			y += fmt.Sprintf("  - host: gone.test\n    type: static\n    root: %s\n    access_log:\n      path: %s\n    ssl:\n      mode: \"off\"\n", root, logPath)
		}
		p := filepath.Join(dir, "uwas.yaml")
		if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfgPath := write(true)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); s.domainLogs.Close() })
	s.SetConfigPath(cfgPath)
	s.handler = s.buildMiddlewareChain()

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "gone.test"
	req.Header.Set("User-Agent", "regression-test")
	s.handler.ServeHTTP(httptest.NewRecorder(), req)

	open := func() bool {
		s.domainLogs.mu.RLock()
		defer s.domainLogs.mu.RUnlock()
		_, ok := s.domainLogs.files[logPath]
		return ok
	}
	if !open() {
		t.Fatal("control: access log not open while its domain exists")
	}
	if err := s.reload(); err != nil || !open() {
		t.Fatalf("reload with the domain still present: err=%v open=%v", err, open())
	}

	write(false)
	if err := s.reload(); err != nil {
		t.Fatal(err)
	}
	if open() {
		t.Fatal("removed domain's access log still open after reload")
	}
}
