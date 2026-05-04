package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func newAuditTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global: {}\n"), 0600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	cfg := &config.Config{}
	cfg.Global.Audit.RecordIP = true
	s := &Server{
		config:     cfg,
		configPath: cfgPath,
		logger:     logger.New("error", "text"),
		metrics:    metrics.New(),
	}
	s.initAudit()
	return s
}

func TestAuditPersist_AppendAndReload(t *testing.T) {
	dir := t.TempDir()

	s1 := newAuditTestServer(t, dir)
	s1.RecordAudit("test.action", "first", "1.1.1.1", true)
	s1.RecordAudit("test.action", "second", "2.2.2.2", false)

	logPath := filepath.Join(dir, "audit.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("audit log file should exist: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
	var first AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 not JSON: %v", err)
	}
	if first.Action != "test.action" || first.Detail != "first" || !first.Success {
		t.Fatalf("first entry mismatch: %+v", first)
	}

	// New server reads same dir → ring buffer should be repopulated.
	s2 := newAuditTestServer(t, dir)
	if err := s2.loadAuditLog(); err != nil {
		t.Fatalf("load: %v", err)
	}
	s2.auditMu.Lock()
	defer s2.auditMu.Unlock()
	if s2.auditPos != 2 {
		t.Errorf("expected auditPos=2 after replay, got %d (full=%v)", s2.auditPos, s2.auditFull)
	}
	if s2.auditEntries[0].Detail != "first" || s2.auditEntries[1].Detail != "second" {
		t.Errorf("entries not replayed in order: %+v", s2.auditEntries[:2])
	}
}

func TestAuditPersist_NoFileWhenNoConfigPath(t *testing.T) {
	cfg := &config.Config{}
	s := &Server{
		config:  cfg,
		logger:  logger.New("error", "text"),
		metrics: metrics.New(),
	}
	s.initAudit()
	// Should not panic, should not crash.
	s.RecordAudit("a", "b", "ip", true)
	if got := s.auditLogFile(); got != "" {
		t.Errorf("expected empty path, got %q", got)
	}
}

func TestAuditPersist_RotationAt10MB(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	// Pre-fill log with > 10MB so a single record triggers rotation.
	big := strings.Repeat("x", auditMaxFileSize+10)
	if err := os.WriteFile(logPath, []byte(big), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := newAuditTestServer(t, dir)
	s.RecordAudit("rotate.me", "now", "1.2.3.4", true)
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("expected rotated file audit.log.1: %v", err)
	}
}

func TestAuditPersist_RecordAuditUserStoresUsername(t *testing.T) {
	dir := t.TempDir()
	s := newAuditTestServer(t, dir)
	s.RecordAuditUser("auth.user.password", "alice", "1.1.1.1", "alice", true)

	s.auditMu.Lock()
	got := s.auditEntries[0]
	s.auditMu.Unlock()

	if got.User != "alice" {
		t.Errorf("expected User=alice, got %q", got.User)
	}
	// Reload and verify the user attribution survives a restart.
	s2 := newAuditTestServer(t, dir)
	if err := s2.loadAuditLog(); err != nil {
		t.Fatalf("load: %v", err)
	}
	s2.auditMu.Lock()
	defer s2.auditMu.Unlock()
	if s2.auditEntries[0].User != "alice" {
		t.Errorf("user attribution lost across reload: %+v", s2.auditEntries[0])
	}
}

func TestAuditPersist_RotationKeepsThreeGenerationsAndReloadsAll(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	// Pre-seed audit.log.3 .. .1 .. .log with one entry each so we can tell
	// the order in the replayed ring buffer.
	mustWriteEntry := func(path, detail string) {
		t.Helper()
		e := AuditEntry{Action: "seed", Detail: detail, Success: true}
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	mustWriteEntry(logPath+".3", "oldest")
	mustWriteEntry(logPath+".2", "older")
	mustWriteEntry(logPath+".1", "old")
	mustWriteEntry(logPath, "newest")

	s := newAuditTestServer(t, dir)
	if err := s.loadAuditLog(); err != nil {
		t.Fatalf("load: %v", err)
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if s.auditPos != 4 {
		t.Fatalf("expected 4 entries replayed, got pos=%d full=%v", s.auditPos, s.auditFull)
	}
	want := []string{"oldest", "older", "old", "newest"}
	for i, w := range want {
		if got := s.auditEntries[i].Detail; got != w {
			t.Errorf("entry %d: got %q, want %q", i, got, w)
		}
	}
}

func TestAuditPersist_RotationDropsOldest(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	// Pre-fill log with > 10MB so the next append triggers rotation.
	big := strings.Repeat("x", auditMaxFileSize+10)
	if err := os.WriteFile(logPath, []byte(big), 0600); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	// Pre-fill .3 with sentinel so we can verify it's the one that gets dropped.
	if err := os.WriteFile(logPath+".3", []byte("dropme"), 0600); err != nil {
		t.Fatalf("seed .3: %v", err)
	}
	if err := os.WriteFile(logPath+".2", []byte("becomes-3"), 0600); err != nil {
		t.Fatalf("seed .2: %v", err)
	}
	if err := os.WriteFile(logPath+".1", []byte("becomes-2"), 0600); err != nil {
		t.Fatalf("seed .1: %v", err)
	}

	s := newAuditTestServer(t, dir)
	s.RecordAudit("rotate.me", "now", "1.2.3.4", true)

	check := func(path, want string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			return
		}
		if !strings.Contains(string(got), want) {
			t.Errorf("%s: expected to contain %q, got %q", path, want, string(got)[:min(40, len(got))])
		}
	}
	check(logPath+".3", "becomes-3")
	check(logPath+".2", "becomes-2")
	// .1 should now be the previous .log content (the 10MB blob).
	if info, err := os.Stat(logPath + ".1"); err != nil || info.Size() < auditMaxFileSize {
		t.Errorf("expected .1 to hold the rotated 10MB log, got err=%v size=%v", err, info)
	}
}

func TestAuditPersist_TailsToMaxAuditEntries(t *testing.T) {
	dir := t.TempDir()
	s1 := newAuditTestServer(t, dir)
	// Write 3x ring buffer worth so tail truncation is exercised.
	for i := 0; i < maxAuditEntries*3; i++ {
		s1.RecordAudit("e", "n", "ip", true)
	}

	s2 := newAuditTestServer(t, dir)
	if err := s2.loadAuditLog(); err != nil {
		t.Fatalf("load: %v", err)
	}
	s2.auditMu.Lock()
	defer s2.auditMu.Unlock()
	if !s2.auditFull {
		t.Error("ring buffer should be marked full after replaying > maxAuditEntries lines")
	}
}
