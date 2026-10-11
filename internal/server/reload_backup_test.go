package server

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2861 / F2862: the backup manager copied its destinations and the list of
// domain content roots at startup. A reload that moved the backup directory
// kept writing to the old one, and a domain added later was missing from every
// backup until restart.

func backupReloadConfig(t *testing.T, dir, backupDir string, withSecond bool) string {
	t.Helper()
	r1 := filepath.Join(dir, "r1")
	r2 := filepath.Join(dir, "elsewhere", "r2")
	for _, r := range []string{r1, r2} {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r1, "marker1.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r2, "marker2.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := ""
	if withSecond {
		second = fmt.Sprintf(`  - host: two.test
    type: static
    root: %s
    ssl:
      mode: "off"
`, r2)
	}
	y := fmt.Sprintf(`global:
  worker_count: "1"
  log_level: error
  log_format: text
  web_root: %s
  backup:
    enabled: true
    provider: local
    keep: 3
    local:
      path: %s
domains:
  - host: one.test
    type: static
    root: %s
    ssl:
      mode: "off"
%s`, filepath.Join(dir, "www"), backupDir, r1, second)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func backupArchives(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "uwas-backup-*.tar.gz"))
	return m
}

func backupEntryNames(t *testing.T, archive string) []string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}

func anyContains(names []string, sub string) bool {
	for _, n := range names {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func newBackupReloadServer(t *testing.T, withSecond bool) (*Server, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	path := backupReloadConfig(t, dir, backupDir, withSecond)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	if s.backupMgr == nil {
		t.Fatal("backup manager not created")
	}
	return s, dir, backupDir, path
}

func TestReloadRefreshesBackupDestination(t *testing.T) {
	s, dir, oldDir, _ := newBackupReloadServer(t, false)

	// Control: a backup before the reload lands in the configured directory.
	if _, err := s.backupMgr.CreateBackup("local"); err != nil {
		t.Fatalf("control backup: %v", err)
	}
	if n := len(backupArchives(oldDir)); n != 1 {
		t.Fatalf("control backup count in %s = %d, want 1", oldDir, n)
	}

	newDir := filepath.Join(dir, "backups-new")
	backupReloadConfig(t, dir, newDir, false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := s.backupMgr.CreateBackup("local"); err != nil {
		t.Fatalf("backup after reload: %v", err)
	}
	if n := len(backupArchives(newDir)); n != 1 {
		t.Errorf("backups in the new directory = %d, want 1 (reload ignored the moved destination)", n)
	}
	if n := len(backupArchives(oldDir)); n != 1 {
		t.Errorf("backups in the old directory = %d, want still 1", n)
	}
}

func TestReloadAddsNewDomainToBackup(t *testing.T) {
	s, dir, backupDir, _ := newBackupReloadServer(t, false)

	if _, err := s.backupMgr.CreateBackup("local"); err != nil {
		t.Fatalf("control backup: %v", err)
	}
	first := backupArchives(backupDir)
	if len(first) != 1 {
		t.Fatalf("control archives = %d, want 1", len(first))
	}
	names := backupEntryNames(t, first[0])
	if !anyContains(names, "marker1.txt") {
		t.Fatalf("control: startup domain content missing from the backup: %v", names)
	}
	if anyContains(names, "marker2.txt") {
		t.Fatalf("control: second domain should not exist yet: %v", names)
	}

	backupReloadConfig(t, dir, backupDir, true)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := s.backupMgr.CreateBackup("local"); err != nil {
		t.Fatalf("backup after reload: %v", err)
	}
	all := backupArchives(backupDir)
	if len(all) != 2 {
		t.Fatalf("archives = %d, want 2", len(all))
	}
	// Archive names carry a nanosecond timestamp; the later one sorts last.
	last := all[0]
	if all[1] > last {
		last = all[1]
	}
	if names := backupEntryNames(t, last); !anyContains(names, "marker2.txt") {
		t.Errorf("domain added by the reload missing from the next backup: %v", names)
	}
}
