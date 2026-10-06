package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestCreateDomainBackupFailsWhenDumpFails pins the no-silent-drop contract:
// when the database dump fails, CreateDomainBackup must fail instead of
// publishing an archive that looks complete without the database.
func TestCreateDomainBackupFailsWhenDumpFails(t *testing.T) {
	orig := dumpDatabaseFunc
	dumpDatabaseFunc = func(dbName string) ([]byte, error) {
		return nil, fmt.Errorf("mysqldump exploded")
	}
	defer func() { dumpDatabaseFunc = orig }()

	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	m := New(config.BackupConfig{}, logger.New("error", "text"))
	m.providers["local"] = NewLocalProvider(dest)

	_, err := m.CreateDomainBackup("example.com", webRoot, "appdb", "local")
	if err == nil {
		t.Fatal("expected the failed database dump to fail CreateDomainBackup; a DB-less archive would be presented as complete")
	}
	if !strings.Contains(err.Error(), "mysqldump exploded") {
		t.Fatalf("error should carry the dump failure, got: %v", err)
	}
}
