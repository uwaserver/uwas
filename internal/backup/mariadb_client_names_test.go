package backup

// F39601: MariaDB 11 installs its client tools as mariadb / mariadb-dump and
// the mysql-named compatibility links are optional. The database manager
// already looks for "mariadb" before "mysql", but backup looked only for
// mysqldump / mysql: on such a host the dump failed (backup written without any
// database) and the restore skipped the database import and reported success.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// onlyClients puts stand-ins for exactly the named binaries on a PATH that
// holds nothing else, so the host's own mysql tools cannot be picked up. The
// stubs print a one-line dump and drain stdin using shell builtins only.
func onlyClients(t *testing.T, names ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	for _, n := range names {
		script := "#!/bin/sh\nwhile read -r _; do :; done\necho 'SELECT 1;'\n"
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestDumpFindsMariaDBClient(t *testing.T) {
	for _, k := range []string{"UWAS_DB_HOST", "UWAS_DB_PORT", "UWAS_DB_USER", "UWAS_DB_PASSWORD"} {
		t.Setenv(k, "")
	}
	onlyClients(t, "mariadb-dump")
	for name, run := range map[string]func() ([]byte, error){
		"single": func() ([]byte, error) { return dumpDatabaseReal("wp_site") },
		"all":    dumpAllDatabasesReal,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := run()
			t.Logf("EXPECTED: dump from mariadb-dump\nACTUAL: out=%q err=%v", out, err)
			if err != nil || !strings.Contains(string(out), "SELECT 1") {
				t.Fatalf("PROBLEM CONFIRMED: no dump with only mariadb-dump installed (err=%v)", err)
			}
		})
	}
}

func TestRestoreFindsMariaDBClient(t *testing.T) {
	for _, k := range []string{"UWAS_DB_HOST", "UWAS_DB_PORT", "UWAS_DB_USER", "UWAS_DB_PASSWORD"} {
		t.Setenv(k, "")
	}
	// The stub records that it ran by writing a marker next to itself.
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := "#!/bin/sh\nwhile read -r _; do :; done\n: > '" + marker + "'\n"
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	if err := os.WriteFile(filepath.Join(dir, "mariadb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := importDatabaseDumpReal([]byte("SELECT 1;"), logger.New("error", "text")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: restore skipped the database import with only mariadb installed: %v", err)
	}
}

// Controls: the mysql-named tools keep working, and are preferred when both
// exist so a host's current behaviour does not change.
func TestMySQLNamedClientsStillUsed(t *testing.T) {
	for _, k := range []string{"UWAS_DB_HOST", "UWAS_DB_PORT", "UWAS_DB_USER", "UWAS_DB_PASSWORD"} {
		t.Setenv(k, "")
	}
	onlyClients(t, "mysqldump")
	if out, err := dumpDatabaseReal("wp_site"); err != nil || !strings.Contains(string(out), "SELECT 1") {
		t.Fatalf("control: mysqldump not used: out=%q err=%v", out, err)
	}
}

func TestNoClientStillReportsError(t *testing.T) {
	for _, k := range []string{"UWAS_DB_HOST", "UWAS_DB_PORT", "UWAS_DB_USER", "UWAS_DB_PASSWORD"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := dumpAllDatabasesReal(); err == nil {
		t.Fatal("control: dump with no client installed must fail")
	}
	// A files-only restore on a host without a client stays non-fatal.
	if err := importDatabaseDumpReal([]byte("SELECT 1;"), logger.New("error", "text")); err != nil {
		t.Fatalf("control: restore without a client must stay non-fatal, got %v", err)
	}
}
