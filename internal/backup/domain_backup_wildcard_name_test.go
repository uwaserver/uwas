package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F36602: the per-domain archive name embedded the host verbatim. A wildcard
// host ("*.example.com") is a valid configured domain, but "*" is outside the
// character set safeBackupFilename accepts, so the archive could be written to
// the provider and then never downloaded, restored or deleted from it.
func TestDomainBackupNameIsSafeForAnyConfiguredHost(t *testing.T) {
	for _, host := range []string{"*.example.com", "example.com"} {
		t.Run(host, func(t *testing.T) {
			webRoot := t.TempDir()
			if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("hi"), 0o644); err != nil {
				t.Fatal(err)
			}
			m := New(config.BackupConfig{}, logger.New("error", "text"))
			m.providers["local"] = NewLocalProvider(t.TempDir())

			info, err := m.CreateDomainBackup(host, webRoot, "", "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("EXPECTED: a name safeBackupFilename accepts\nACTUAL: %s", info.Name)
			if err := safeBackupFilename(info.Name); err != nil {
				t.Fatalf("PROBLEM CONFIRMED: %q is not addressable on remote providers: %v", info.Name, err)
			}
		})
	}
}
