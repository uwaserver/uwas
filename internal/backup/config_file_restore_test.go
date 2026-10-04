package backup

import (
	"archive/tar"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"os"
	"path/filepath"
	"testing"
)

func configFileRestoreRestore(t *testing.T, file bool, data string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	path := dir
	target := filepath.Join(dir, "uwas.yaml")
	if file {
		if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		path = target
	}
	m := New(config.BackupConfig{}, logger.New("error", "text"))
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	mp.files["test.tar.gz"] = buildArchive(t, []tarEntry{{name: "config/uwas.yaml", data: []byte(data), typeflag: tar.TypeReg}})
	m.SetPaths(path, "")
	err := m.RestoreBackup("test.tar.gz", "mem")
	got, readErr := os.ReadFile(target)
	if err == nil && readErr != nil {
		t.Fatal(readErr)
	}
	return string(got), err
}
func TestRestoreWithExistingConfigFilePath(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, data := range []string{"restored", "", "replacement again"} {
			got, err := configFileRestoreRestore(t, file, data)
			if err != nil || got != data {
				t.Fatalf("file=%v data=%q got=%q err=%v", file, data, got, err)
			}
		}
	}
}
