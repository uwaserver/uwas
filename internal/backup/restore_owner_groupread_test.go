package backup

// F44002: a restore hands new files to the owner of the tree they land in
// (F43001) but kept the archive's mode. WordPress's wp-config.php is archived
// 0600: restored into a tenant tree (owner = the site's SFTP user, group = the
// web account) it was readable only by that user, and the PHP worker — the web
// account since F42700 — could not read its own config. New files in a handed
// over tree must be readable by the owning group, as siteuser makes
// wp-config.php (0640) when it creates the account.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func buildModeArchive(t *testing.T, files map[string]os.FileMode) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, mode := range files {
		data := []byte("<?php // " + name)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: int64(mode), Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func restoreModes(t *testing.T, asRoot bool, existing map[string]os.FileMode, archive map[string]os.FileMode) string {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("needs an unprivileged test user: a root-owned tree is never handed over")
	}
	recordOwnerCalls(t, asRoot)
	webRoot, cfgDir := t.TempDir(), t.TempDir()
	site := filepath.Join(webRoot, "site.test")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, mode := range existing {
		p := filepath.Join(site, name)
		if err := os.WriteFile(p, []byte("old"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	m := New(config.BackupConfig{}, logger.New("error", "text"))
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	files := map[string]os.FileMode{}
	for name, mode := range archive {
		files["sites/site.test/"+name] = mode
	}
	mp.files["site.tar.gz"] = buildModeArchive(t, files)
	m.SetPaths(cfgDir, "")
	m.SetDomainPaths(webRoot, "", nil)
	if err := m.RestoreBackup("site.tar.gz", "mem"); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	return site
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestRestoredOwnerOnlyFileIsReadableByTheWebGroupUnderRoot(t *testing.T) {
	site := restoreModes(t, true, nil, map[string]os.FileMode{"wp-config.php": 0o600, "index.php": 0o644})
	got := perm(t, filepath.Join(site, "wp-config.php"))
	t.Logf("EXPECTED: wp-config.php group-readable (0640)\nACTUAL: %#o", got)
	if got&0o040 == 0 {
		t.Fatalf("PROBLEM CONFIRMED: restored wp-config.php is %#o, the web account's group cannot read it", got)
	}
	if got&0o007 != 0 {
		t.Errorf("wp-config.php is %#o: must stay closed to other users", got)
	}
	if p := perm(t, filepath.Join(site, "index.php")); p != 0o644 {
		t.Errorf("index.php = %#o, want 0644 unchanged", p)
	}
}

// Controls: a replaced file keeps the mode it has...
func TestRestoreKeepsTheModeOfAReplacedFile(t *testing.T) {
	site := restoreModes(t, true, map[string]os.FileMode{"wp-config.php": 0o600}, map[string]os.FileMode{"wp-config.php": 0o600})
	if p := perm(t, filepath.Join(site, "wp-config.php")); p != 0o600 {
		t.Errorf("a replaced file changed mode: %#o, want 0600", p)
	}
}

// ...and an unprivileged UWAS keeps the archive's mode.
func TestUnprivilegedRestoreKeepsTheArchiveMode(t *testing.T) {
	site := restoreModes(t, false, nil, map[string]os.FileMode{"wp-config.php": 0o600})
	if p := perm(t, filepath.Join(site, "wp-config.php")); p != 0o600 {
		t.Errorf("an unprivileged restore changed the archive mode: %#o, want 0600", p)
	}
}
