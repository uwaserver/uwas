package backup

// F39600: UWAS_DB_HOST / UWAS_DB_PORT / UWAS_DB_USER / UWAS_DB_PASSWORD point
// the backup at a remote or containerized database, and the restore path
// (importDatabaseDumpReal) honours them. The dump path did not: mysqldump ran
// with no connection arguments, so on such a deployment it looked for a local
// socket that does not exist, failed, and the backup was written without any
// database while the restore side was configured to import one.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// fakeMySQLClient installs a stand-in for bin on PATH that records its argv and
// MYSQL_PWD, and prints a one-line SQL dump. It returns the record file path.
func fakeMySQLClient(t *testing.T, bin string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, bin+".rec")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + rec + "'\n" +
		"printf 'MYSQL_PWD=%s\\n' \"$MYSQL_PWD\" >> '" + rec + "'\n" +
		"cat >/dev/null\n" +
		"echo 'SELECT 1;'\n"
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return rec
}

func setRemoteDBEnv(t *testing.T) {
	t.Helper()
	t.Setenv("UWAS_DB_HOST", "db.internal")
	t.Setenv("UWAS_DB_PORT", "3307")
	t.Setenv("UWAS_DB_USER", "backup")
	t.Setenv("UWAS_DB_PASSWORD", "s3cret-pw")
}

func readRecord(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("stub was not run: %v", err)
	}
	return string(b)
}

func TestDumpUsesRemoteConnectionSettings(t *testing.T) {
	setRemoteDBEnv(t)
	for name, run := range map[string]func() error{
		"single database": func() error { _, err := dumpDatabaseReal("wp_site"); return err },
		"all databases":   func() error { _, err := dumpAllDatabasesReal(); return err },
	} {
		t.Run(name, func(t *testing.T) {
			rec := fakeMySQLClient(t, "mysqldump")
			if err := run(); err != nil {
				t.Fatal(err)
			}
			got := readRecord(t, rec)
			t.Logf("EXPECTED: --host=db.internal --port=3307 --user=backup, password only in MYSQL_PWD\nACTUAL:\n%s", got)
			for _, want := range []string{"--host=db.internal", "--port=3307", "--user=backup", "MYSQL_PWD=s3cret-pw"} {
				if !strings.Contains(got, want) {
					t.Errorf("PROBLEM CONFIRMED: mysqldump invocation lacks %q", want)
				}
			}
			args := got[:strings.Index(got, "MYSQL_PWD=")]
			if strings.Contains(args, "s3cret-pw") {
				t.Errorf("password leaked on argv:\n%s", args)
			}
		})
	}
}

// Control: the restore side already honours the same settings.
func TestImportUsesRemoteConnectionSettings(t *testing.T) {
	setRemoteDBEnv(t)
	rec := fakeMySQLClient(t, "mysql")
	if err := importDatabaseDumpReal([]byte("SELECT 1;"), logger.New("error", "text")); err != nil {
		t.Fatal(err)
	}
	got := readRecord(t, rec)
	for _, want := range []string{"--host=db.internal", "--port=3307", "--user=backup", "MYSQL_PWD=s3cret-pw"} {
		if !strings.Contains(got, want) {
			t.Errorf("control: restore invocation lacks %q:\n%s", want, got)
		}
	}
}

// Control: with no UWAS_DB_* settings the dump keeps its socket-based form and
// the database name stays the last argument.
func TestDumpWithoutRemoteSettingsIsUnchanged(t *testing.T) {
	for _, k := range []string{"UWAS_DB_HOST", "UWAS_DB_PORT", "UWAS_DB_USER", "UWAS_DB_PASSWORD"} {
		t.Setenv(k, "")
	}
	rec := fakeMySQLClient(t, "mysqldump")
	if _, err := dumpDatabaseReal("wp_site"); err != nil {
		t.Fatal(err)
	}
	got := readRecord(t, rec)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	want := []string{"--single-transaction", "--quick", "wp_site", "MYSQL_PWD="}
	if len(lines) != len(want) {
		t.Fatalf("argv/env = %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}
