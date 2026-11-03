package database

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeDockerWithUsers puts a `docker` on PATH that answers `inspect` with a
// MariaDB image and reports every account in existing as present when asked
// about mysql.user.
func fakeDockerWithUsers(t *testing.T, existing ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake docker")
	}
	var cases strings.Builder
	for _, u := range existing {
		cases.WriteString("      *\"User = '" + u + "'\"*) echo 1;;\n")
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = inspect ]; then echo mariadb:11; exit 0; fi\n" +
		"if [ \"$1\" = exec ]; then\n" +
		"  sql=$(cat)\n" +
		"  case \"$sql\" in\n" +
		"  *'FROM mysql.user'*)\n" +
		"    case \"$sql\" in\n" + cases.String() + "      *) echo 0;;\n    esac;;\n" +
		"  esac\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func dockerCreateDB(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.SetPathValue("name", "uwas-db-x")
	rec := httptest.NewRecorder()
	New(&guardDeps{}).DockerCreateDatabase(rec, req)
	return rec
}

// F21901: an existing account on the Docker path answers 409 like the native
// path (F11900), not a 500 that leaks the internal error text.
func TestDockerCreateDatabaseExistingUserIsConflict(t *testing.T) {
	fakeDockerWithUsers(t, "siteb")

	// control: a fresh account is created
	if rec := dockerCreateDB(t, `{"name":"sitea","user":"sitea","password":"pw-a"}`); rec.Code != http.StatusCreated {
		t.Fatalf("control: status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	rec := dockerCreateDB(t, `{"name":"sitea2","user":"siteb","password":"pw-new"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("EXPECTED: 409 for an existing account\nACTUAL: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "container") {
		t.Errorf("conflict response leaks internal detail: %s", rec.Body.String())
	}
}

func TestDockerCreateDatabaseExistingSchemaIsConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake docker")
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = inspect ]; then echo mariadb:11; exit 0; fi\n" +
		"if [ \"$1\" = exec ]; then\n" +
		"  sql=$(cat)\n" +
		"  case \"$sql\" in *information_schema.SCHEMATA*) echo 1;; *) echo 0;; esac\n" +
		"fi\n" +
		"exit 0\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rec := dockerCreateDB(t, `{"name":"existing","user":"fresh","password":"pw"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("existing schema status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "container") {
		t.Errorf("conflict response leaks internal detail: %s", rec.Body.String())
	}
}
