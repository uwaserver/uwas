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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// TestDomainAccessLogPruneBackupsCountsPairAsOneBackup pins the grouping rule
// that pruneBackups documents at domainlog.go:335-342. compressFile holds
// "<base>.<ts>" and its "<base>.<ts>.gz" on disk simultaneously for the whole
// copy window, and rotateLocked starts compression and pruning concurrently.
// Counting the pair as two backups let the keep-slice absorb the in-flight
// source and evict an archive still inside the retention window.
//
// The state below reproduces exactly that window: rotation 3 is mid-copy (its
// source exists, its .gz does not yet), while rotations 1 and 2 are complete
// (both forms on disk). With MaxBackups=2 the correct answer keeps rotations 3
// and 2 — including rotation 2's SOURCE. An ungrouped count sees five files,
// keeps the two newest by name (rotation 3's source and rotation 2's .gz) and
// deletes rotation 2's source, orphaning the archive it belongs to.
func TestDomainAccessLogPruneBackupsCountsPairAsOneBackup(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	seed := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	// Rotation 1 and 2: compressed and source both present.
	for _, ts := range []string{"20200101-000000.000000000", "20200102-000000.000000000"} {
		seed("access.log."+ts+".gz", "archive\n")
		seed("access.log."+ts, "source\n")
	}
	// Rotation 3: still being copied — source only, .gz not created yet.
	seed("access.log.20200103-000000.000000000", "in-flight source\n")

	pruneBackups(base, 2)

	mustExist := func(name, why string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s (%s): %v", name, why, err)
		}
	}
	mustBeGone := func(name, why string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned (%s), stat err = %v", name, why, err)
		}
	}

	mustExist("access.log.20200103-000000.000000000", "newest rotation, in-flight")
	mustExist("access.log.20200102-000000.000000000", "second-newest rotation, kept as one backup")
	mustExist("access.log.20200102-000000.000000000.gz", "archive of the kept rotation, not orphaned")
	mustBeGone("access.log.20200101-000000.000000000", "oldest rotation")
	mustBeGone("access.log.20200101-000000.000000000.gz", "oldest rotation's archive")
}

// TestDomainAccessLogRotationHonorsMaxBackupsCap drives the real rotation path
// past MaxBackups and asserts the cap holds: only the newest N archives
// survive. MaxSize=1 makes every Write trip the size check, so the number of
// rotations is the number of requests.
func TestDomainAccessLogRotationHonorsMaxBackupsCap(t *testing.T) {
	const secret = "CAPSECRET"
	const maxBackups = 2
	const requests = 8

	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	s, h := newDomainLogServer(t, logPath, config.RotateConfig{
		MaxSize:    config.ByteSize(1), // rotate on every write
		MaxBackups: maxBackups,
		MaxAge:     config.Duration{Duration: time.Hour},
	})

	for i := 0; i < requests; i++ {
		getThroughChain(h, "/?token="+secret)
	}

	// Close waits on m.bg, where both compressFile and pruneBackups run.
	s.domainLogs.Close()

	archives, err := filepath.Glob(filepath.Join(dir, "access.log.*.gz"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(archives) > maxBackups {
		t.Errorf("retention cap breached: %d archives survived after %d rotations with MaxBackups=%d:\n%v",
			len(archives), requests, maxBackups, archives)
	}
	if len(archives) == 0 {
		t.Fatalf("expected archives after %d rotations with MaxSize=1; rotation never fired", requests)
	}

	// Whatever survived the cap must still be redacted.
	if body := gunzipAll(t, dir, "access.log"); strings.Contains(body, secret) {
		t.Errorf("secret leaked into a retained archive: %q", body)
	}
}

// TestDomainAccessLogJSONFormatRotationIsRedacted covers the json access-log
// format on the rotation path. accessLogLine emits a different encoding for
// json than for the CLF default, so redaction has to be shown on both.
func TestDomainAccessLogJSONFormatRotationIsRedacted(t *testing.T) {
	const secret = "JSONSECRET"

	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "warn", LogFormat: "text"},
		Domains: []config.Domain{{
			Host: "log.test",
			Type: "static",
			Root: t.TempDir(),
			SSL:  config.SSLConfig{Mode: "off"},
			AccessLog: config.AccessLogConfig{Path: logPath, BufferSize: 0, Format: "json",
				Rotate: config.RotateConfig{
					MaxSize:    config.ByteSize(512),
					MaxBackups: 10,
					MaxAge:     config.Duration{Duration: time.Hour},
				}},
		}},
	}
	s := New(cfg, logger.New("warn", "text"))
	s.cancel()
	h := s.buildMiddlewareChain()

	for i := 0; i < 12; i++ {
		getThroughChain(h, "/?token="+secret)
	}
	s.domainLogs.Close()

	archives, err := filepath.Glob(filepath.Join(dir, "access.log.*.gz"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(archives) == 0 {
		t.Fatalf("expected a rotated json archive in %s; rotation never fired", dir)
	}

	body := gunzipAll(t, dir, "access.log")
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("json archive decompressed to nothing")
	}

	// Each line must parse as json and carry a redacted path -- this asserts
	// both the format contract and the redaction inside that format.
	for i, line := range lines {
		var entry struct {
			Path string `json:"path"`
			Time string `json:"time"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("json archive line %d is not valid json: %v\n%q", i, err, line)
		}
		if entry.Time == "" {
			t.Errorf("json archive line %d has no time field: %q", i, line)
		}
		if strings.Contains(entry.Path, secret) {
			t.Errorf("json archive line %d leaked the secret in path: %q", i, entry.Path)
		}
		if !strings.Contains(entry.Path, "REDACTED") {
			t.Errorf("json archive line %d expected a redacted path, got %q", i, entry.Path)
		}
	}
}

// TestDomainAccessLogCleanupOldMaxAgeBoundary pins the exact MaxAge comparison
// in cleanupOld. That test needs an injected clock: the real one advances
// between seeding an mtime and running the sweep, so "aged exactly MaxAge"
// is unrepresentable and the boundary would silently drift to whatever the
// scheduler allowed.
func TestDomainAccessLogCleanupOldMaxAgeBoundary(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	const maxAge = 24 * time.Hour
	fixed := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	// atBoundary is aged to exactly MaxAge; pastBoundary to MaxAge+1s.
	atBoundary := filepath.Join(dir, "access.log.20260314-120000.000000000")
	pastBoundary := filepath.Join(dir, "access.log.20260314-115959.000000000")
	seed := func(p string, mod time.Time) {
		if err := os.WriteFile(p, []byte("GET / HTTP/1.1\n"), 0o640); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}
	seed(atBoundary, fixed.Add(-maxAge))
	seed(pastBoundary, fixed.Add(-maxAge).Add(-time.Second))

	orig := domainLogNow
	domainLogNow = func() time.Time { return fixed }
	defer func() { domainLogNow = orig }()

	m := &domainLogManager{files: map[string]*domainLogFile{
		"log.test": {
			path:   base,
			rotate: config.RotateConfig{MaxAge: config.Duration{Duration: maxAge}},
		},
	}}
	m.cleanupOld()

	// cleanupOld uses a strict `>`, so a file aged exactly MaxAge is NOT
	// expired and must survive.
	if _, err := os.Stat(atBoundary); err != nil {
		t.Errorf("cleanupOld removed a log aged exactly MaxAge (%v); the comparison must be strict `>`, not `>=`", err)
	}
	if _, err := os.Stat(pastBoundary); !os.IsNotExist(err) {
		t.Errorf("cleanupOld kept a log aged MaxAge+1s (stat err = %v)", err)
	}
}

// TestDomainAccessLogCompressFileCopyErrorKeepsOriginal drives compressFile's
// io.Copy failure branch. os.Open succeeds on a directory, but reading the fd
// fails with EISDIR -- so this reaches the copy error without any seam.
func TestDomainAccessLogCompressFileCopyErrorKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "access.log.20260315-120000.000000000")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", src, err)
	}

	compressFile(src)

	if _, err := os.Stat(src + ".gz"); !os.IsNotExist(err) {
		t.Errorf("compressFile left a partial .gz behind after a copy failure (stat err = %v)", err)
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		t.Errorf("compressFile disturbed the source after a copy failure (stat err = %v)", err)
	}
}

// TestDomainAccessLogCompressFileGzipCloseErrorKeepsOriginal drives
// compressFile's gz.Close() failure branch. /dev/full returns ENOSPC on every
// write, so with an EMPTY source io.Copy writes nothing and succeeds -- making
// the trailer flush inside gz.Close() the first and only failing write.
func TestDomainAccessLogCompressFileGzipCloseErrorKeepsOriginal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is Linux-specific")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "access.log.20260315-120000.000000000")
	if err := os.WriteFile(src, nil, 0o640); err != nil {
		t.Fatalf("write empty source: %v", err)
	}
	// os.Create follows the symlink and opens /dev/full, which always fails a
	// write with ENOSPC.
	if err := os.Symlink("/dev/full", src+".gz"); err != nil {
		t.Fatalf("symlink to /dev/full: %v", err)
	}

	compressFile(src)

	// This is the contract the branch comment states: a corrupt archive must
	// never cost us the only copy. If the gz.Close() error were ignored,
	// control would fall through to os.Remove(path) and destroy the log.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("compressFile deleted the original log after gz.Close() failed (%v); "+
			"a failed trailer flush must not cost the only copy of the data", err)
	}
	if _, err := os.Stat(src + ".gz"); !os.IsNotExist(err) {
		t.Errorf("compressFile left the corrupt archive behind (stat err = %v)", err)
	}
}
