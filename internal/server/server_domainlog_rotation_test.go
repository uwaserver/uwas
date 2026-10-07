package server

// Durable coverage for the two retention paths in internal/server/domainlog.go.
//
// The write sink is redacted (server_dispatch.go passes middleware.SanitizeURI
// to domainLogs.Write). These tests pin that the retention paths preserve that
// property, which the earlier tests did not touch:
//
//   - rotateLocked renames the active log to <path>.<ts>, gzips it in a
//     goroutine, prunes backups, and opens a fresh file. The archive is a byte
//     copy of what was written, so it must contain no secret.
//   - cleanupOld only *deletes* rotated files past MaxAge -- it cannot "carry
//     redacted paths" at all. What is worth pinning is that it removes exactly
//     the expired archives, leaves in-window ones and the active log alone, and
//     that the active log which survives it still holds only redacted lines.
//
// Both run the real handleRequest -> domainLogs.Write path so the redaction
// under test is the production one, not a string built by the test.

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// newDomainLogServer returns a server whose domain writes an access log to
// logPath, plus a request helper that drives the real middleware chain.
func newDomainLogServer(t *testing.T, logPath string, rotate config.RotateConfig) (*Server, http.Handler) {
	t.Helper()

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "warn", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:      "log.test",
			Type:      "static",
			Root:      t.TempDir(),
			SSL:       config.SSLConfig{Mode: "off"},
			AccessLog: config.AccessLogConfig{Path: logPath, BufferSize: 0, Rotate: rotate},
		}},
	}
	s := New(cfg, logger.New("warn", "text"))
	s.cancel()
	return s, s.buildMiddlewareChain()
}

func getThroughChain(h http.Handler, target string) {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "log.test"
	// botguard 403s an empty User-Agent; irrelevant here, but keep the chain
	// behaving as it does in production.
	req.Header.Set("User-Agent", "regression-test")
	h.ServeHTTP(httptest.NewRecorder(), req)
}

// gunzipAll reads every *.gz under dir and returns the concatenated contents.
func gunzipAll(t *testing.T, dir, base string) string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, base+".*.gz"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var sb strings.Builder
	for _, m := range matches {
		f, err := os.Open(m)
		if err != nil {
			t.Fatalf("open %s: %v", m, err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			t.Fatalf("gzip reader %s: %v", m, err)
		}
		b, err := io.ReadAll(gz)
		f.Close()
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		sb.Write(b)
	}
	return sb.String()
}

// TestDomainAccessLogRotatedArchiveIsRedacted drives enough traffic to trip
// rotateLocked, waits for the async gzip via Close, then decompresses every
// archive and asserts the secret never reached disk.
func TestDomainAccessLogRotatedArchiveIsRedacted(t *testing.T) {
	const secret = "ROTATIONSECRET"

	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	s, h := newDomainLogServer(t, logPath, config.RotateConfig{
		// Small enough that a handful of requests trips rotation (each CLF
		// line is ~120 bytes); 10 backups so pruneBackups cannot evict the
		// archive before we read it.
		MaxSize:    config.ByteSize(512),
		MaxBackups: 10,
		MaxAge:     config.Duration{Duration: time.Hour},
	})

	for i := 0; i < 12; i++ {
		getThroughChain(h, "/?token="+secret)
	}

	// Close waits on m.bg, which is what compressFile and pruneBackups run
	// under -- without it the archive may not exist yet. Close is called once:
	// it closes m.stop, so a second call would panic.
	s.domainLogs.Close()

	archives, err := filepath.Glob(filepath.Join(dir, "access.log.*.gz"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(archives) == 0 {
		t.Fatalf("expected at least one rotated archive in %s; rotation never fired", dir)
	}

	body := gunzipAll(t, dir, "access.log")
	if body == "" {
		t.Fatal("rotated archive(s) decompressed to nothing")
	}
	if strings.Contains(body, secret) {
		t.Fatalf("secret leaked into a rotated access log archive: %q", body)
	}
	if !strings.Contains(body, "REDACTED") {
		t.Errorf("expected the archive to hold a redacted path, got %q", body)
	}
}

// TestDomainAccessLogCleanupOldRemovesOnlyExpired covers cleanupOld. It cannot
// itself carry a path, so what is pinned is that it deletes exactly the
// archives past MaxAge, spares the in-window one and the active log, and that
// the surviving active log still contains only redacted lines.
func TestDomainAccessLogCleanupOldRemovesOnlyExpired(t *testing.T) {
	const secret = "CLEANUPSECRET"

	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	s, h := newDomainLogServer(t, logPath, config.RotateConfig{
		// Large enough that no rotation fires during this test: the archives
		// are created by hand so their timestamps are controllable.
		MaxSize:    config.ByteSize(1 << 20),
		MaxBackups: 10,
		MaxAge:     config.Duration{Duration: time.Hour},
	})
	defer s.domainLogs.Close()

	// One real request so Write registers the file and the active log exists.
	getThroughChain(h, "/?token="+secret)

	expired := filepath.Join(dir, "access.log.20200101-000000.000000000")
	fresh := filepath.Join(dir, "access.log.20990101-000000.000000000")
	for _, p := range []string{expired, fresh} {
		if err := os.WriteFile(p, []byte("stale-archive\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(expired, old, old); err != nil {
		t.Fatalf("backdate expired archive: %v", err)
	}

	s.domainLogs.cleanupOld()

	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("expired archive survived cleanupOld (expected removal), stat err = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("in-window archive was removed by cleanupOld: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("cleanupOld removed the ACTIVE log: %v", err)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read active log: %v", err)
	}
	if strings.Contains(string(body), secret) {
		t.Errorf("secret in the active log that survived cleanupOld: %q", body)
	}
	if !strings.Contains(string(body), "REDACTED") {
		t.Errorf("expected the surviving active log to hold a redacted path, got %q", body)
	}
}
