package database

// F41600: input the database layer rejects as invalid answered 500 (and, for
// Create, a generic "database creation failed" that hid the reason). The
// sibling DropUser and the explorer already answer 400.
// F41601: the Docker create endpoint answered 500 for an unsupported engine, an
// out-of-range port and an already-existing container; the first two are
// request errors and the last is a conflict.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	dbpkg "github.com/uwaserver/uwas/internal/database"
)

// realCreateDeps routes CreateDB to the real validator, which rejects a bad
// name before it would run any SQL.
type realCreateDeps struct{ guardDeps }

func (d *realCreateDeps) CreateDB(name, user, password, host string) (*dbpkg.CreateResult, error) {
	return dbpkg.CreateDatabase(name, user, password, host)
}

func TestCreateInvalidIdentifierIsBadRequest(t *testing.T) {
	for _, body := range []string{
		`{"name":"my-db"}`,
		`{"name":"shop","user":"bad-user"}`,
		`{"name":"shop","password":"a\u0000b"}`,
	} {
		rec := httptest.NewRecorder()
		New(&realCreateDeps{}).Create(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		t.Logf("EXPECTED: 400 with the reason\nACTUAL: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PROBLEM CONFIRMED: %s -> %d, want 400", body, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "database creation failed") {
			t.Errorf("%s: the validation reason is hidden behind a generic message: %s", body, rec.Body.String())
		}
	}
}

func TestChangePasswordInvalidUserIsBadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	New(&guardDeps{}).ChangePassword(rec, httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"user":"bad-user","host":"localhost","password":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PROBLEM CONFIRMED: invalid user -> %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

func TestDockerCreateRequestErrorsAreBadRequest(t *testing.T) {
	cases := []struct{ name, body string }{
		{"unsupported engine", `{"name":"x","engine":"oracle","port":3307,"root_pass":"p"}`},
		{"port too large", `{"name":"x","engine":"mariadb","port":70000,"root_pass":"p"}`},
		{"negative port", `{"name":"x","engine":"mariadb","port":-1,"root_pass":"p"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(&guardDeps{}).DockerCreate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body)))
			t.Logf("EXPECTED: 400\nACTUAL: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("PROBLEM CONFIRMED: %s -> %d, want 400", c.name, rec.Code)
			}
		})
	}
}

func TestDockerCreateExistingContainerIsConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake docker")
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = info ]; then echo 24.0.0; exit 0; fi\n" +
		"if [ \"$1\" = ps ]; then echo uwas-db-taken; exit 0; fi\n" +
		"exit 0\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rec := httptest.NewRecorder()
	New(&guardDeps{}).DockerCreate(rec, httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"taken","engine":"mariadb","port":3307,"root_pass":"p"}`)))
	t.Logf("EXPECTED: 409\nACTUAL: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != http.StatusConflict {
		t.Errorf("PROBLEM CONFIRMED: existing container -> %d, want 409", rec.Code)
	}

	// control: a free name proceeds past the existence check (the fake docker
	// answers every other call with success)
	script2 := "#!/bin/sh\nif [ \"$1\" = info ]; then echo 24.0.0; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script2), 0o755); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	New(&guardDeps{}).DockerCreate(rec, httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"free","engine":"mariadb","port":3307,"root_pass":"p"}`)))
	if rec.Code == http.StatusConflict || rec.Code == http.StatusBadRequest {
		t.Errorf("control: free name -> %d, want it to pass validation (%s)", rec.Code, rec.Body.String())
	}
}

func TestDockerDBNameErrorsAreBadRequest(t *testing.T) {
	// the validators reject these before any docker call runs
	for _, c := range []struct {
		name string
		call func(*Handler, http.ResponseWriter, *http.Request)
		body string
		path map[string]string
	}{
		{"create database", (*Handler).DockerCreateDatabase, `{"name":"bad-db","user":"u","password":"p"}`, map[string]string{"name": "uwas-db-x"}},
		{"export", (*Handler).DockerExport, ``, map[string]string{"name": "uwas-db-x", "db": "shop.prod"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body))
			for k, v := range c.path {
				req.SetPathValue(k, v)
			}
			rec := httptest.NewRecorder()
			c.call(New(&guardDeps{}), rec, req)
			t.Logf("EXPECTED: 400\nACTUAL: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("PROBLEM CONFIRMED: %s with an invalid database name -> %d, want 400", c.name, rec.Code)
			}
		})
	}
}
