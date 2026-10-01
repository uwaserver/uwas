package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

func TestDomainLogWrite(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	m := newDomainLogManager()
	defer m.Close()

	m.Write("example.com", config.AccessLogConfig{Path: logPath, Rotate: config.RotateConfig{}},
		"GET", "/index.html", "127.0.0.1", "TestAgent",
		200, 1024, 5*time.Millisecond)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "127.0.0.1") {
		t.Error("expected remote IP in log")
	}
	if !strings.Contains(content, "GET /index.html") {
		t.Error("expected request line in log")
	}
	if !strings.Contains(content, "200") {
		t.Error("expected status code in log")
	}
	if !strings.Contains(content, "TestAgent") {
		t.Error("expected user agent in log")
	}
}

func TestDomainLogRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")

	m := newDomainLogManager()
	defer m.Close()

	// Use tiny max size to trigger rotation
	rotate := config.RotateConfig{
		MaxSize:    config.ByteSize(200),
		MaxBackups: 3,
	}

	// Write enough to trigger rotation
	for i := 0; i < 10; i++ {
		m.Write("example.com", config.AccessLogConfig{Path: logPath, Rotate: rotate},
			"GET", "/page", "127.0.0.1", "Agent",
			200, 100, time.Millisecond)
	}

	// Wait for background compression. A fixed sleep is a race: the gzip runs
	// on a background goroutine and 500ms is not enough on a loaded machine.
	waitForRotation(t, dir)

	// Check that the active log exists
	if _, err := os.Stat(logPath); err != nil {
		t.Error("expected active log file to exist after rotation")
	}

	// Check that rotated files exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var rotated int
	for _, e := range entries {
		if e.Name() != "access.log" && strings.HasPrefix(e.Name(), "access.log.") {
			rotated++
		}
	}
	if rotated == 0 {
		t.Error("expected at least 1 rotated file")
	}
}

func TestDomainLogMultipleDomains(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.log")
	pathB := filepath.Join(dir, "b.log")

	m := newDomainLogManager()
	defer m.Close()

	m.Write("a.com", config.AccessLogConfig{Path: pathA, Rotate: config.RotateConfig{}},
		"GET", "/a", "10.0.0.1", "A", 200, 50, time.Millisecond)
	m.Write("b.com", config.AccessLogConfig{Path: pathB, Rotate: config.RotateConfig{}},
		"POST", "/b", "10.0.0.2", "B", 201, 75, time.Millisecond)

	dataA, _ := os.ReadFile(pathA)
	dataB, _ := os.ReadFile(pathB)

	if !strings.Contains(string(dataA), "10.0.0.1") {
		t.Error("domain A log should contain its own IP")
	}
	if !strings.Contains(string(dataB), "10.0.0.2") {
		t.Error("domain B log should contain its own IP")
	}
}

func TestDomainLogEmptyPath(t *testing.T) {
	m := newDomainLogManager()
	defer m.Close()

	// Should not panic with empty path
	m.Write("example.com", config.AccessLogConfig{Path: "", Rotate: config.RotateConfig{}},
		"GET", "/", "127.0.0.1", "Agent", 200, 0, time.Millisecond)
}

func TestFindRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	// Create some rotated files
	os.WriteFile(base, []byte("active"), 0644)
	os.WriteFile(base+".20260101-120000.gz", []byte("old1"), 0644)
	os.WriteFile(base+".20260102-120000.gz", []byte("old2"), 0644)

	rotated := findRotatedFiles(base)
	if len(rotated) != 2 {
		t.Errorf("expected 2 rotated files, got %d", len(rotated))
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	// Create 5 rotated files
	for i := 1; i <= 5; i++ {
		name := filepath.Join(dir, "access.log.2026010"+string(rune('0'+i))+"-120000.gz")
		os.WriteFile(name, []byte("data"), 0644)
	}

	pruneBackups(base, 2)

	rotated := findRotatedFiles(base)
	if len(rotated) != 2 {
		t.Errorf("expected 2 rotated files after prune, got %d", len(rotated))
	}
}

// TestPruneBackupsCountsInFlightCompressionAsOneBackup pins the retention
// contract against a rotation whose gzip is still being written.
//
// compressFile keeps "access.log.<ts>" and "access.log.<ts>.gz" on disk at
// the same time — it unlinks the source only after the archive is closed —
// and rotateLocked starts it alongside this prune, so a prune routinely runs
// inside that window. The pair is one rotated log, not two. Counting it as
// two made the keep-slice absorb the in-flight source and evict an archive
// that was still inside the max_backups window, so the configured retention
// silently lost a log.
func TestPruneBackupsCountsInFlightCompressionAsOneBackup(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	// T1, T2: compression finished, only the archive remains.
	for _, name := range []string{
		"access.log.20260101-000000.000000000.gz",
		"access.log.20260102-000000.000000000.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// T3: compressFile is mid-copy, so the source and its archive coexist.
	for _, name := range []string{
		"access.log.20260103-000000.000000000.gz",
		"access.log.20260103-000000.000000000",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	active := filepath.Join(dir, "access.log")
	if err := os.WriteFile(active, []byte("data"), 0644); err != nil {
		t.Fatalf("write active log: %v", err)
	}

	pruneBackups(base, 2)

	// Three rotated logs exist, so the newest two (T3, T2) are inside the
	// retention window. T2's archive must survive.
	if _, err := os.Stat(filepath.Join(dir, "access.log.20260102-000000.000000000.gz")); err != nil {
		t.Errorf("backup inside the max_backups=2 window was pruned: %v", err)
	}
	// T1 is outside the window and is the one that must go.
	if _, err := os.Stat(filepath.Join(dir, "access.log.20260101-000000.000000000.gz")); err == nil {
		t.Error("backup outside the max_backups=2 window was kept")
	}
	// The active log is never a rotated backup.
	if _, err := os.Stat(active); err != nil {
		t.Errorf("prune removed the active log: %v", err)
	}
}

// TestPruneBackupsRetainsBackupWhoseCompressionFailed covers the secondary
// branch: a rotation with no archive (compressFile bailed) is a single file
// that must still count as one backup and be retained on its own merit.
func TestPruneBackupsRetainsBackupWhoseCompressionFailed(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	// Newest rotation failed to compress: source only, no .gz.
	failed := filepath.Join(dir, "access.log.20260103-000000.000000000")
	if err := os.WriteFile(failed, []byte("data"), 0644); err != nil {
		t.Fatalf("write failed-compression backup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "access.log.20260102-000000.000000000.gz"), []byte("data"), 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "access.log.20260101-000000.000000000.gz"), []byte("data"), 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	pruneBackups(base, 2)

	if _, err := os.Stat(failed); err != nil {
		t.Errorf("backup whose compression failed was pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "access.log.20260102-000000.000000000.gz")); err != nil {
		t.Errorf("backup inside the max_backups=2 window was pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "access.log.20260101-000000.000000000.gz")); err == nil {
		t.Error("backup outside the max_backups=2 window was kept")
	}
}

// TestPruneBackupsUnderLimitDeletesNothing is the boundary case: when the
// number of rotated logs is at or below max_backups, nothing may be removed —
// including an in-flight pair, which counts as one.
func TestPruneBackupsUnderLimitDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "access.log")

	names := []string{
		"access.log.20260102-000000.000000000.gz",
		"access.log.20260103-000000.000000000.gz",
		"access.log.20260103-000000.000000000", // in-flight twin of the above
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	pruneBackups(base, 2) // two rotated logs, max_backups: 2

	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("prune deleted %s at the max_backups limit: %v", name, err)
		}
	}
}

// waitForRotation waits until at least one rotated log appears, instead of
// sleeping for a fixed time and hoping the background compression finished.
func waitForRotation(t *testing.T, dir string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if e.Name() != "access.log" && strings.HasPrefix(e.Name(), "access.log.") {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rotation did not complete within 10 seconds")
}
