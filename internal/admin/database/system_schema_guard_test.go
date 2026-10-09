package database

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/uwaserver/uwas/internal/database"
)

type guardDeps struct {
	drops, creates int
}

func (d *guardDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool { return true }
func (d *guardDeps) RequirePin(http.ResponseWriter, *http.Request) bool   { return true }
func (d *guardDeps) LogInfo(string, ...any)                               {}
func (d *guardDeps) LogDebug(string, ...any)                              {}
func (d *guardDeps) LogError(string, ...any)                              {}
func (d *guardDeps) RecordAudit(*http.Request, string, string, bool)      {}
func (d *guardDeps) ParsePagination(*http.Request) (int, int)             { return 50, 0 }
func (d *guardDeps) TaskActive() *TaskInfo                                { return nil }
func (d *guardDeps) TaskSubmit(string, string, string, func(func(string)) error) *TaskInfo {
	return &TaskInfo{}
}
func (d *guardDeps) StartService() error             { return nil }
func (d *guardDeps) StopService() error              { return nil }
func (d *guardDeps) RestartService() error           { return nil }
func (d *guardDeps) RepairService() (string, error)  { return "", nil }
func (d *guardDeps) Uninstall() (string, error)      { return "", nil }
func (d *guardDeps) ForceUninstall() (string, error) { return "", nil }
func (d *guardDeps) CreateDB(name, user, _, host string) (*dbpkg.CreateResult, error) {
	d.creates++
	return &dbpkg.CreateResult{Name: name, User: user, Host: host}, nil
}
func (d *guardDeps) DropDB(string, string, string) error { d.drops++; return nil }

// Drop reuses the database name as the user it drops, so DELETE
// /databases/root would issue DROP USER 'root'@'localhost' and
// DELETE /databases/mysql would drop the grant tables — the protected
// accounts DropUser refuses. Create/Import must not target system schemas.
func TestSystemSchemaAndProtectedUserRefused(t *testing.T) {
	for _, name := range []string{"root", "mysql", "sys", "performance_schema", "information_schema", "MySQL"} {
		d := &guardDeps{}
		req := httptest.NewRequest(http.MethodDelete, "/", nil)
		req.SetPathValue("name", name)
		rec := httptest.NewRecorder()
		New(d).Drop(rec, req)
		if rec.Code != http.StatusBadRequest || d.drops != 0 {
			t.Errorf("Drop %q: status=%d drops=%d, want 400 and no drop", name, rec.Code, d.drops)
		}
	}
	for _, body := range []string{`{"name":"mysql","user":"x","host":"%"}`, `{"name":"shop","user":"root","host":"%"}`} {
		d := &guardDeps{}
		rec := httptest.NewRecorder()
		New(d).Create(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest || d.creates != 0 {
			t.Errorf("Create %s: status=%d creates=%d, want 400 and no create", body, rec.Code, d.creates)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("DROP TABLE user;"))
	req.SetPathValue("name", "mysql")
	rec := httptest.NewRecorder()
	New(&guardDeps{}).Import(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Import mysql: status=%d, want 400", rec.Code)
	}

	d := &guardDeps{}
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	req.SetPathValue("name", "mysql_app")
	rec = httptest.NewRecorder()
	New(d).Drop(rec, req)
	if rec.Code != http.StatusOK || d.drops != 1 {
		t.Errorf("Drop mysql_app: status=%d drops=%d, want 200 and one drop", rec.Code, d.drops)
	}
}
