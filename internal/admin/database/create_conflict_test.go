package database

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/uwaserver/uwas/internal/database"
)

type existingUserDeps struct{ guardDeps }

func (d *existingUserDeps) CreateDB(name, user, _, host string) (*dbpkg.CreateResult, error) {
	return nil, fmt.Errorf("create database %q: %w: %s@%s", name, dbpkg.ErrUserExists, user, host)
}

type existingSchemaDeps struct{ guardDeps }

func (d *existingSchemaDeps) CreateDB(name, user, _, host string) (*dbpkg.CreateResult, error) {
	return nil, fmt.Errorf("create database %q: %w", name, dbpkg.ErrDatabaseExists)
}

// F11900: an existing account answers 409 instead of a generic 500 so the
// operator picks another user name.
func TestCreateExistingUserIsConflict(t *testing.T) {
	rec := httptest.NewRecorder()
	New(&existingUserDeps{}).Create(rec, httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"shop","user":"shared","host":"localhost"}`)))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreateExistingSchemaIsConflict(t *testing.T) {
	rec := httptest.NewRecorder()
	New(&existingSchemaDeps{}).Create(rec, httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"name":"shop","user":"shop_user","host":"localhost"}`)))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// F11901: the password of root and the other protected accounts is not
// changeable through the panel (the refusal comes before any SQL runs).
func TestChangePasswordRefusesProtectedAccount(t *testing.T) {
	for _, user := range []string{"root", "mysql", "debian-sys-maint", "mariadb.sys"} {
		rec := httptest.NewRecorder()
		New(&guardDeps{}).ChangePassword(rec, httptest.NewRequest(http.MethodPost, "/",
			strings.NewReader(`{"user":"`+user+`","host":"localhost","password":"x"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", user, rec.Code, rec.Body.String())
		}
	}
}
