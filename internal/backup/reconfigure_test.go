package backup

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2861: destinations and retention were fixed when the manager was built.

func reconfigureManager(t *testing.T, dir string) *BackupManager {
	t.Helper()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(config.BackupConfig{Provider: "local", Keep: 3, Local: config.BackupLocalConfig{Path: filepath.Join(dir, "a")}}, logger.New("error", "text"))
	m.SetPaths(cfgPath, "")
	return m
}

func TestReconfigureSwapsLocalDestination(t *testing.T) {
	dir := t.TempDir()
	m := reconfigureManager(t, dir)
	if _, err := m.CreateBackup("local"); err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "a", "uwas-backup-*.tar.gz")); len(got) != 1 {
		t.Fatalf("control: %d archives in a/, want 1", len(got))
	}

	m.Reconfigure(config.BackupConfig{Provider: "local", Keep: 3, Local: config.BackupLocalConfig{Path: filepath.Join(dir, "b")}})
	if _, err := m.CreateBackup("local"); err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "b", "uwas-backup-*.tar.gz")); len(got) != 1 {
		t.Errorf("%d archives in b/ after Reconfigure, want 1", len(got))
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "a", "uwas-backup-*.tar.gz")); len(got) != 1 {
		t.Errorf("%d archives in a/ after Reconfigure, want still 1", len(got))
	}
}

func TestReconfigureAddsAndDropsProviders(t *testing.T) {
	m := New(config.BackupConfig{}, logger.New("error", "text"))
	if m.Provider("s3") != nil {
		t.Fatal("control: s3 provider registered without a bucket")
	}
	m.Reconfigure(config.BackupConfig{S3: config.BackupS3Config{Bucket: "b", Endpoint: "http://127.0.0.1:1", Region: "r"}})
	if m.Provider("s3") == nil {
		t.Error("s3 provider not registered after Reconfigure")
	}
	if m.Provider("local") == nil {
		t.Error("local provider lost after Reconfigure")
	}
	m.Reconfigure(config.BackupConfig{})
	if m.Provider("s3") != nil {
		t.Error("s3 provider still registered after it was removed from the config")
	}
}

func TestReconfigureRetention(t *testing.T) {
	m := New(config.BackupConfig{Keep: 5}, logger.New("error", "text"))
	m.Reconfigure(config.BackupConfig{Keep: 2})
	m.mu.Lock()
	got := m.keepCount
	m.mu.Unlock()
	if got != 2 {
		t.Errorf("keepCount = %d, want 2", got)
	}
	m.Reconfigure(config.BackupConfig{})
	m.mu.Lock()
	got = m.keepCount
	m.mu.Unlock()
	if got != 7 {
		t.Errorf("keepCount without a configured value = %d, want the default 7", got)
	}
}

// Reconfigure racing backups, listings and provider lookups must be safe.
func TestReconfigureRace(t *testing.T) {
	dir := t.TempDir()
	m := reconfigureManager(t, dir)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				m.Reconfigure(config.BackupConfig{Provider: "local", Keep: 3, Local: config.BackupLocalConfig{Path: filepath.Join(dir, "r", string(rune('a'+j%2)))}})
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				_ = m.ListBackups()
				_ = m.Provider("local")
				_, _ = m.CreateBackup("local")
			}
		}()
	}
	close(start)
	wg.Wait()
}
