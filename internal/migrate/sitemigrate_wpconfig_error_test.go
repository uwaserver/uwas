package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateSurfacesWPConfigUpdateFailure(t *testing.T) {
	oldSync, oldDB, oldChown := runSyncFiles, runMigrateDB, runChown
	t.Cleanup(func() {
		runSyncFiles, runMigrateDB, runChown = oldSync, oldDB, oldChown
	})
	runSyncFiles = func(MigrateRequest, *strings.Builder) string { return "ok" }
	runMigrateDB = func(MigrateRequest, *strings.Builder) string { return "ok" }
	runChown = func(string) error { return nil }

	root := t.TempDir()
	target := filepath.Join(root, "outside-config.php")
	config := "<?php\ndefine('DB_NAME', 'old');\ndefine('DB_USER', 'old');\ndefine('DB_PASSWORD', 'old');\ndefine('DB_HOST', 'old');\n"
	if err := os.WriteFile(target, []byte(config), 0600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "wp-config.php")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	result := Migrate(MigrateRequest{
		SourceHost: "user@source.test",
		SourcePort: "22",
		SourcePath: "/var/www/site",
		LocalRoot:  root,
		Domain:     "site.test",
		DBName:     "site_db",
		DBUser:     "site_user",
		DBPass:     "new-secret",
	})
	if result.Error == "" || !strings.Contains(result.Error, "wp-config") {
		t.Fatalf("Migrate() error = %q, want wp-config update failure", result.Error)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != config {
		t.Fatalf("symlink target changed: err=%v content=%q", err, data)
	}
}
