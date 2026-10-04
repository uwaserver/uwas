package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func atomicRestoreArchive(t *testing.T, truncated bool, data string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	size := int64(len(data))
	if truncated {
		size += 5
	}
	if err := tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Typeflag: tar.TypeReg, Mode: 0644, Size: size}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if !truncated {
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func atomicRestoreCase(t *testing.T, existing, truncated bool, data string, limit int64) (string, bool, int) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "uwas.yaml")
	if existing {
		if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := New(config.BackupConfig{MaxFileSize: limit}, logger.New("error", "text"))
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	mp.files["ordinary.tar.gz"] = atomicRestoreArchive(t, truncated, data)
	m.SetPaths(dir, "")
	err := m.RestoreBackup("ordinary.tar.gz", "mem")
	got, readErr := os.ReadFile(target)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if err != nil && !existing && !os.IsNotExist(readErr) {
		t.Fatal("failed restore created a target file")
	}
	if existing || err == nil {
		info, statErr := os.Stat(target)
		if statErr != nil {
			t.Fatal(statErr)
		}
		wantMode := os.FileMode(0644)
		if existing {
			wantMode = 0600
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("mode=%o want=%o", info.Mode().Perm(), wantMode)
		}
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	temps := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restore-") {
			temps++
		}
	}
	return string(got), err != nil, temps
}
func TestRestorePreservesFilesOnReadAndLimitFailure(t *testing.T) {
	for _, existing := range []bool{true, false} {
		for _, truncated := range []bool{true, false} {
			limit := int64(3)
			if truncated {
				limit = 100
			}
			got, failed, temps := atomicRestoreCase(t, existing, truncated, "partial", limit)
			want := ""
			if existing {
				want = "original"
			}
			if !failed || got != want || temps != 0 {
				t.Fatalf("existing=%v truncated=%v got=%s failed=%v temps=%d", existing, truncated, got, failed, temps)
			}
		}
	}
	for _, existing := range []bool{true, false} {
		for _, data := range []string{"", "complete"} {
			got, failed, temps := atomicRestoreCase(t, existing, false, data, 100)
			if failed || got != data || temps != 0 {
				t.Fatal("successful restore", got, failed, temps)
			}
		}
	}
}
