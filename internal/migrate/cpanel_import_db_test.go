package migrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The migration page's "Import databases" checkbox (on by default) posts
// import_db=true, but ImportCPanelBackup never read the flag: dumps were only
// listed with imported=false, and the temporary extraction directory they
// lived in was deleted on return, so the databases were silently lost.

type cpanelDBFake struct {
	mu       sync.Mutex
	calls    int
	imported map[string]string // database -> dump bytes fed on stdin
	created  []string
}

// installCPanelDBFake stubs the mariadb/mysql client. tables maps a database
// to the table count the existence probe reports (missing = 0).
func installCPanelDBFake(t *testing.T, tables map[string]string) *cpanelDBFake {
	t.Helper()
	f := &cpanelDBFake{imported: map[string]string{}}
	dumpDir := t.TempDir()
	origLook, origExec := execLookPathFn, execCommandFn
	t.Cleanup(func() { execLookPathFn, execCommandFn = origLook, origExec })
	execLookPathFn = func(n string) (string, error) { return "/fake/" + n, nil }
	execCommandFn = func(_ string, args ...string) *exec.Cmd {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "information_schema.tables"):
			for db, n := range tables {
				if strings.Contains(joined, "'"+db+"'") {
					return exec.Command("sh", "-c", "printf '"+n+"'")
				}
			}
			return exec.Command("sh", "-c", "printf '0'")
		case strings.Contains(joined, "CREATE DATABASE"):
			f.created = append(f.created, joined)
			return exec.Command("sh", "-c", "exit 0")
		case len(args) == 3 && args[0] == "-u" && args[1] == "root":
			out := filepath.Join(dumpDir, args[2])
			f.imported[args[2]] = out
			return exec.Command("sh", "-c", "cat > '"+out+"'")
		}
		return exec.Command("sh", "-c", "exit 0")
	}
	return f
}

func cpanelDBArchive(t *testing.T) string {
	t.Helper()
	arc := filepath.Join(t.TempDir(), "cpmove-alice.tar.gz")
	writeCPanelBackup(t, arc, map[string]string{
		"cpmove-alice/userdata/main":                  "main_domain: main.test\n",
		"cpmove-alice/homedir/public_html/index.html": "MAIN",
		"cpmove-alice/mysql/alice_wp.sql":             "CREATE TABLE a(i int);\n",
		"cpmove-alice/mysql/alice_old.sql":            "CREATE TABLE b(i int);\n",
		"cpmove-alice/mysql/bad-name.sql":             "CREATE TABLE c(i int);\n",
	})
	return arc
}

func dbByName(res *CPanelResult, name string) *CPanelDatabase {
	for i := range res.Databases {
		if res.Databases[i].Name == name {
			return &res.Databases[i]
		}
	}
	return nil
}

func TestCPanelImportLoadsDatabasesWhenAsked(t *testing.T) {
	f := installCPanelDBFake(t, map[string]string{"alice_old": "3"})
	res, err := ImportCPanelBackup(cpanelDBArchive(t), t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	wp := dbByName(res, "alice_wp")
	if wp == nil || !wp.Imported {
		t.Fatalf("EXPECTED: alice_wp imported\nACTUAL: %+v errors=%v", wp, res.Errors)
	}
	dump, _ := os.ReadFile(f.imported["alice_wp"])
	if string(dump) != "CREATE TABLE a(i int);\n" {
		t.Errorf("dump fed to the client = %q", dump)
	}
	if len(f.created) != 1 || !strings.Contains(f.created[0], "`alice_wp`") {
		t.Errorf("CREATE DATABASE calls = %v, want exactly one for alice_wp", f.created)
	}
	// Never overwrite a database that already has tables.
	if old := dbByName(res, "alice_old"); old == nil || old.Imported {
		t.Errorf("alice_old (3 tables) = %+v, want listed but not imported", old)
	}
	if _, ok := f.imported["alice_old"]; ok {
		t.Error("alice_old was loaded over an existing database")
	}
	// An invalid identifier is reported, not passed to SQL.
	if bad := dbByName(res, "bad-name"); bad == nil || bad.Imported {
		t.Errorf("bad-name = %+v, want listed but not imported", bad)
	}
	if _, ok := f.imported["bad-name"]; ok {
		t.Error("an invalid database name reached the client")
	}
	joined := strings.Join(res.Errors, "\n")
	for _, want := range []string{"alice_old", "bad-name"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors do not mention %s: %v", want, res.Errors)
		}
	}
	for _, d := range res.Databases {
		if d.SQLFile != "" {
			t.Errorf("%s: sql_file %q points into the deleted extraction directory", d.Name, d.SQLFile)
		}
	}
}

// Control: without the flag nothing touches the database server.
func TestCPanelImportLeavesDatabasesAloneByDefault(t *testing.T) {
	f := installCPanelDBFake(t, nil)
	res, err := ImportCPanelBackup(cpanelDBArchive(t), t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Errorf("client invoked %d times with import_db=false", f.calls)
	}
	if len(res.Databases) != 3 {
		t.Errorf("databases listed = %d, want 3", len(res.Databases))
	}
	for _, d := range res.Databases {
		if d.Imported {
			t.Errorf("%s marked imported with import_db=false", d.Name)
		}
	}
}

func TestCPanelImportReportsMissingDatabaseClient(t *testing.T) {
	origLook := execLookPathFn
	t.Cleanup(func() { execLookPathFn = origLook })
	execLookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
	res, err := ImportCPanelBackup(cpanelDBArchive(t), t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Errors, "\n"), "client") {
		t.Errorf("a missing mariadb/mysql client was not reported: %v", res.Errors)
	}
}
