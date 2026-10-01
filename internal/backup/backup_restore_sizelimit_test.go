package backup

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// max_file_size is an inclusive maximum: config.BackupConfig.MaxFileSize is
// documented as "max bytes per file" (internal/config/backup.go:10), so a file
// of exactly that size is within the limit and must restore.
//
// RestoreBackup copied through io.LimitReader(tr, maxFileSize), which makes
// `written` unable to exceed the limit, then rejected on `written >= maxFileSize`.
// A file of exactly the limit therefore tripped the guard and the whole restore
// aborted partway, leaving a half-restored tree. The database-dump branch of the
// same function already had it right: read one byte past the limit and reject
// only on strictly-greater.
func TestRestoreAcceptsFileExactlyAtMaxFileSize(t *testing.T) {
	const limit = 4096

	m := restoreManagerAtLimit(t, limit)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	atLimit := bytes.Repeat([]byte("A"), limit)
	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "config/uwas.yaml", data: atLimit, typeflag: tar.TypeReg},
	})

	dstDir := t.TempDir()
	configBase := filepath.Join(dstDir, "cfg")
	m.SetPaths(configBase, "")

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatalf("a file of exactly max_file_size (%d bytes) was rejected: %v", limit, err)
	}
	got, err := os.ReadFile(filepath.Join(configBase, "uwas.yaml"))
	if err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
	if len(got) != limit {
		t.Errorf("restored %d bytes, want %d", len(got), limit)
	}
}

// TestRestoreRejectsFileOverMaxFileSize is the boundary companion: one byte over
// must still be rejected, so the guard cannot simply be deleted.
func TestRestoreRejectsFileOverMaxFileSize(t *testing.T) {
	const limit = 4096

	for _, over := range []int{limit + 1, limit * 4} {
		m := restoreManagerAtLimit(t, limit)
		mp := newMemoryProvider("mem")
		m.providers["mem"] = mp

		mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
			{name: "config/uwas.yaml", data: bytes.Repeat([]byte("A"), over), typeflag: tar.TypeReg},
		})

		dstDir := t.TempDir()
		m.SetPaths(filepath.Join(dstDir, "cfg"), "")

		if err := m.RestoreBackup("t.tar.gz", "mem"); err == nil {
			t.Errorf("a %d-byte file (limit %d) was accepted; over-limit must be rejected", over, limit)
		}
	}
}

// TestRestoreAcceptsFilesUnderMaxFileSize is the control for the ordinary
// sub-limit case, which must be unaffected by the inclusive-boundary change.
func TestRestoreAcceptsFilesUnderMaxFileSize(t *testing.T) {
	const limit = 4096

	for _, size := range []int{1, 100, limit - 1} {
		m := restoreManagerAtLimit(t, limit)
		mp := newMemoryProvider("mem")
		m.providers["mem"] = mp

		mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
			{name: "config/uwas.yaml", data: bytes.Repeat([]byte("A"), size), typeflag: tar.TypeReg},
		})

		dstDir := t.TempDir()
		configBase := filepath.Join(dstDir, "cfg")
		m.SetPaths(configBase, "")

		if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
			t.Errorf("a %d-byte file (limit %d) was rejected: %v", size, limit, err)
			continue
		}
		got, err := os.ReadFile(filepath.Join(configBase, "uwas.yaml"))
		if err != nil {
			t.Errorf("%d-byte file not restored: %v", size, err)
			continue
		}
		if len(got) != size {
			t.Errorf("%d-byte file restored as %d bytes", size, len(got))
		}
	}
}

// TestRestoreAtLimitFileDoesNotAbortArchive covers the user-visible consequence:
// one at-limit entry must not stop the entries after it from restoring.
func TestRestoreAtLimitFileDoesNotAbortArchive(t *testing.T) {
	const limit = 4096

	m := restoreManagerAtLimit(t, limit)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	certsDir := filepath.Join(t.TempDir(), "certs")
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		t.Fatal(err)
	}

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "certs/before.pem", data: []byte("before"), typeflag: tar.TypeReg},
		{name: "certs/at-limit.bin", data: bytes.Repeat([]byte("B"), limit), typeflag: tar.TypeReg},
		{name: "certs/after.pem", data: []byte("after"), typeflag: tar.TypeReg},
	})

	m.SetPaths(filepath.Join(t.TempDir(), "cfg"), certsDir)

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatalf("restore aborted on an at-limit file: %v", err)
	}
	for name, want := range map[string]string{
		"before.pem":   "before",
		"after.pem":    "after",
		"at-limit.bin": string(bytes.Repeat([]byte("B"), limit)),
	} {
		got, err := os.ReadFile(filepath.Join(certsDir, name))
		if err != nil {
			t.Errorf("%s was not restored: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s has %d bytes, want %d", name, len(got), len(want))
		}
	}
}

// restoreManagerAtLimit pins max_file_size, which testManager leaves at 0 —
// RestoreBackup then substitutes the 500MB default, putting the at-limit
// fixture far below the real limit.
func restoreManagerAtLimit(t *testing.T, limit int64) *BackupManager {
	t.Helper()
	m, _ := testManager(t)
	m.cfg.MaxFileSize = limit
	return m
}
