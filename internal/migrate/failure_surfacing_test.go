package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const prodWPConfig = `<?php
define('DB_NAME', 'prod_db');
define('DB_USER', 'prod_user');
define('DB_PASSWORD', 'prod_pass');
define('DB_HOST', 'localhost');
require_once ABSPATH . 'wp-settings.php';
`

// F62000: a failed rsync was recorded in files_sync but the migration still
// finished as "done" — the database was imported, the (partial) tree chowned
// and wp-config rewritten, and the panel showed "Migration Complete".
func TestMigrateFileSyncFailureIsAnError(t *testing.T) {
	origSync, origDB, origChown := runSyncFiles, runMigrateDB, runChown
	defer func() { runSyncFiles, runMigrateDB, runChown = origSync, origDB, origChown }()
	dbCalled := false
	runSyncFiles = func(MigrateRequest, *strings.Builder) string { return "error: exit status 23" }
	runMigrateDB = func(MigrateRequest, *strings.Builder) string { dbCalled = true; return "ok" }
	runChown = func(string) error { return nil }

	root := t.TempDir()
	cfg := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(cfg, []byte(prodWPConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Migrate(MigrateRequest{SourceHost: "u@h.example", SourcePath: "/var/www/a", LocalRoot: root,
		Domain: "a.test", DBName: "newdb", DBUser: "newuser", DBPass: "newpass"})
	if res.Status != "error" || !strings.Contains(res.Error, "file sync failed") {
		t.Fatalf("status = %q error = %q, want error mentioning the file sync failure", res.Status, res.Error)
	}
	if dbCalled {
		t.Error("database import ran after the file sync failed")
	}
	if got, _ := os.ReadFile(cfg); string(got) != prodWPConfig {
		t.Errorf("wp-config.php was rewritten after a failed sync:\n%s", got)
	}
}

// Controls: a successful sync still finishes "done"; a failed database import
// keeps the files but is surfaced in Error for API callers.
func TestMigrateDBFailureIsSurfacedAndSuccessStaysDone(t *testing.T) {
	origSync, origDB, origChown := runSyncFiles, runMigrateDB, runChown
	defer func() { runSyncFiles, runMigrateDB, runChown = origSync, origDB, origChown }()
	runSyncFiles = func(MigrateRequest, *strings.Builder) string { return "ok" }
	runChown = func(string) error { return nil }
	req := MigrateRequest{SourceHost: "u@h.example", SourcePath: "/var/www/a", LocalRoot: t.TempDir(),
		Domain: "a.test", DBName: "newdb", DBUser: "newuser", DBPass: "newpass"}

	runMigrateDB = func(MigrateRequest, *strings.Builder) string { return "ok" }
	if res := Migrate(req); res.Status != "done" || res.Error != "" {
		t.Fatalf("success: status = %q error = %q, want done and no error", res.Status, res.Error)
	}

	runMigrateDB = func(MigrateRequest, *strings.Builder) string { return "error: import failed" }
	res := Migrate(req)
	if res.Status != "done" || !strings.Contains(res.Error, "database import failed") {
		t.Fatalf("db failure: status = %q error = %q, want done with the database failure in Error", res.Status, res.Error)
	}
}

// F62001: when the database clone failed the target keeps the wp-config.php
// the file copy carried over — the SOURCE site's production credentials — and
// the clone is registered as a live domain. The staging site then reads and
// writes the production database. The target must be pointed at its own
// (missing) database so a failure is visible instead.
func TestCloneDBFailureDoesNotLeaveStagingOnProductionDB(t *testing.T) {
	origFiles, origDB, origChown := runCloneFiles, runCloneDB, runCloneChown
	defer func() { runCloneFiles, runCloneDB, runCloneChown = origFiles, origDB, origChown }()
	runCloneFiles = func(src, dst string, _ *strings.Builder) error {
		data, err := os.ReadFile(filepath.Join(src, "wp-config.php"))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, "wp-config.php"), data, 0o600)
	}
	runCloneDB = func(string, string, string, string, *strings.Builder) error {
		return fmt.Errorf("mysqldump not found")
	}
	runCloneChown = func(string) {}

	src, dst := cloneTestRoots(t)
	if err := os.WriteFile(filepath.Join(src, "wp-config.php"), []byte(prodWPConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Clone(CloneRequest{SourceDomain: "a.test", TargetDomain: "staging.a.test", SourceRoot: src, TargetRoot: dst,
		SourceDB: "prod_db", TargetDB: "staging_db", DBUser: "stg_user", DBPass: "stg_pass"})
	if !strings.Contains(res.Error, "database clone failed") {
		t.Fatalf("error = %q, want the database failure surfaced", res.Error)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "wp-config.php"))
	for _, secret := range []string{"prod_db", "prod_user", "prod_pass"} {
		if strings.Contains(string(got), secret) {
			t.Errorf("target wp-config.php still carries the source's %q:\n%s", secret, got)
		}
	}
	if !strings.Contains(string(got), "'staging_db'") {
		t.Errorf("target wp-config.php does not point at the target database:\n%s", got)
	}
}
