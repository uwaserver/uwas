package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The audit log persists to audit.log next to the config file, but the file's
// path is only known once SetConfigPath runs — New() calls loadAuditLog() with
// an empty configPath and loads nothing. If SetConfigPath does not reload it,
// the on-disk history is never replayed into the ring buffer, and the panel
// shows an empty audit log after every restart even though entries keep being
// written. This pins the reload.
func TestSetConfigPathReloadsAuditLog(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global: {}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Simulate a previous run having written history to disk.
	prior := []AuditEntry{
		{Time: time.Now().Add(-time.Hour), Action: "settings.update", Detail: "3 fields", Success: true},
		{Time: time.Now().Add(-time.Minute), Action: "domain.create", Detail: "example.com", Success: true},
	}
	f, err := os.Create(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("create audit.log: %v", err)
	}
	for _, e := range prior {
		line, _ := json.Marshal(e)
		f.Write(append(line, '\n'))
	}
	f.Close()

	s := testServer()
	// Fresh buffer starts empty (New ran loadAuditLog with no path).
	if got := len(s.auditBuf.Snapshot()); got != 0 {
		t.Fatalf("expected empty buffer before SetConfigPath, got %d", got)
	}

	s.SetConfigPath(cfgPath)

	got := s.auditBuf.Snapshot()
	if len(got) != len(prior) {
		t.Fatalf("after SetConfigPath the audit buffer has %d entries, want %d — history not reloaded", len(got), len(prior))
	}
	if got[0].Action != "settings.update" || got[1].Action != "domain.create" {
		t.Fatalf("reloaded entries out of order or wrong: %+v", got)
	}
}
