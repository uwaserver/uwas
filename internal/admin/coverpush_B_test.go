package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/database"
)

// grpBReq builds a request with admin context and an optional path value.
func grpBReq(method, body string) *http.Request {
	r := httptest.NewRequest(method, "/x", strings.NewReader(body))
	return withAdminContext(r)
}

// =============================================================================
// requireAdmin 403 (reseller) for every database handler. The testMux injects
// admin automatically, so we call handlers directly with a reseller context to
// hit the 403 branch.
// =============================================================================

func TestGrpB_DatabaseHandlers_ResellerForbidden(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()

	handlers := map[string]func(http.ResponseWriter, *http.Request){
		"DBStatus":         s.handleDBStatus,
		"DBList":           s.handleDBList,
		"DBCreate":         s.handleDBCreate,
		"DBUsers":          s.handleDBUsers,
		"DBRemoteAccess":   s.handleDBRemoteAccess,
		"DBUninstall":      s.handleDBUninstall,
		"DBStart":          s.handleDBStart,
		"DBStop":           s.handleDBStop,
		"DBRestart":        s.handleDBRestart,
		"DockerDBList":     s.handleDockerDBList,
		"DockerDBCreate":   s.handleDockerDBCreate,
		"DockerDBCreateDB": s.handleDockerDBCreateDatabase,
		"DockerDBExport":   s.handleDockerDBExport,
		"DBExploreTables":  s.handleDBExploreTables,
		"DBExploreColumns": s.handleDBExploreColumns,
		"DBExploreQuery":   s.handleDBExploreQuery,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := withResellerContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}")))
			h(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s reseller = %d, want 403", name, rec.Code)
			}
		})
	}
}

// =============================================================================
// handleDBCreate: validation + seam-backed success/error
// =============================================================================

func TestGrpB_DBCreateInvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBCreate(rec, grpBReq("POST", "{bad"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}
}

func TestGrpB_DBCreateMissingName(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBCreate(rec, grpBReq("POST", `{"user":"u"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
}

func TestGrpB_DBCreateSuccess(t *testing.T) {
	orig := databaseCreateDatabase
	defer func() { databaseCreateDatabase = orig }()
	databaseCreateDatabase = func(name, user, password, host string) (*database.CreateResult, error) {
		return &database.CreateResult{Name: name, User: user, Host: host}, nil
	}
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBCreate(rec, grpBReq("POST", `{"name":"mydb","user":"u","password":"p","host":"localhost"}`))
	if rec.Code != 200 {
		t.Fatalf("create = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mydb") {
		t.Errorf("body missing name: %s", rec.Body.String())
	}
}

func TestGrpB_DBCreateError(t *testing.T) {
	orig := databaseCreateDatabase
	defer func() { databaseCreateDatabase = orig }()
	databaseCreateDatabase = func(name, user, password, host string) (*database.CreateResult, error) {
		return nil, errors.New("boom")
	}
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBCreate(rec, grpBReq("POST", `{"name":"mydb"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("create error = %d, want 500", rec.Code)
	}
}

// =============================================================================
// service control: start/stop/restart seam-backed success + error
// =============================================================================

func TestGrpB_DBServiceControl(t *testing.T) {
	origStart, origStop, origRestart := databaseStartService, databaseStopService, databaseRestartService
	defer func() {
		databaseStartService, databaseStopService, databaseRestartService = origStart, origStop, origRestart
	}()

	databaseStartService = func() error { return nil }
	databaseStopService = func() error { return nil }
	databaseRestartService = func() error { return nil }

	for name, h := range map[string]func(http.ResponseWriter, *http.Request){
		"start":   func(w http.ResponseWriter, r *http.Request) { h := testServer().handleDBStart; h(w, r) },
		"stop":    func(w http.ResponseWriter, r *http.Request) { h := testServer().handleDBStop; h(w, r) },
		"restart": func(w http.ResponseWriter, r *http.Request) { h := testServer().handleDBRestart; h(w, r) },
	} {
		rec := httptest.NewRecorder()
		h(rec, grpBReq("POST", ""))
		if rec.Code != 200 {
			t.Errorf("%s success = %d, want 200", name, rec.Code)
		}
	}

	// error path
	databaseStartService = func() error { return errors.New("nope") }
	rec := httptest.NewRecorder()
	testServer().handleDBStart(rec, grpBReq("POST", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("start error = %d, want 500", rec.Code)
	}
}

// =============================================================================
// handleDBUninstall: requirePin + seam-backed success/error
// =============================================================================

func TestGrpB_DBUninstall(t *testing.T) {
	orig := databaseUninstall
	defer func() { databaseUninstall = orig }()

	// success
	databaseUninstall = func() (string, error) { return "done", nil }
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBUninstall(rec, grpBReq("POST", ""))
	if rec.Code != 200 {
		t.Fatalf("uninstall = %d body=%s", rec.Code, rec.Body.String())
	}

	// error
	databaseUninstall = func() (string, error) { return "", errors.New("fail") }
	rec = httptest.NewRecorder()
	s.handleDBUninstall(rec, grpBReq("POST", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("uninstall error = %d, want 500", rec.Code)
	}
}

func TestGrpB_DBUninstallPinRequired(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.PinCode = "1234"
	rec := httptest.NewRecorder()
	s.handleDBUninstall(rec, grpBReq("POST", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("pin required = %d, want 403", rec.Code)
	}
}

// =============================================================================
// handleDBRemoteAccess: pin + validation
// =============================================================================

func TestGrpB_DBRemoteAccessPinRequired(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.PinCode = "1234"
	rec := httptest.NewRecorder()
	s.handleDBRemoteAccess(rec, grpBReq("POST", `{"user":"u"}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("pin missing = %d, want 403", rec.Code)
	}
}

func TestGrpB_DBRemoteAccessInvalidPin(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.PinCode = "1234"
	rec := httptest.NewRecorder()
	req := grpBReq("POST", `{"user":"u"}`)
	req.Header.Set("X-Pin-Code", "9999")
	s.handleDBRemoteAccess(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("invalid pin = %d, want 403", rec.Code)
	}
}

func TestGrpB_DBRemoteAccessInvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBRemoteAccess(rec, grpBReq("POST", "{bad"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}
}

func TestGrpB_DBRemoteAccessMissingUser(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBRemoteAccess(rec, grpBReq("POST", `{"host":"%"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing user = %d, want 400", rec.Code)
	}
}

// =============================================================================
// handleDBList: not-installed returns empty 200
// =============================================================================

func TestGrpB_DBListNotInstalled(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDBList(rec, grpBReq("GET", ""))
	if rec.Code != 200 {
		t.Fatalf("list = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("expected empty list: %s", rec.Body.String())
	}
}

// =============================================================================
// Docker DB handlers: docker-unavailable branches
// =============================================================================

func TestGrpB_DockerDBListUnavailable(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleDockerDBList(rec, grpBReq("GET", ""))
	// docker not available in test env -> docker:false 200
	if rec.Code != 200 {
		t.Fatalf("docker list = %d", rec.Code)
	}
}

func TestGrpB_DockerDBCreateValidation(t *testing.T) {
	s := testServer()
	// docker unavailable -> 503, OR if available, invalid JSON -> 400.
	rec := httptest.NewRecorder()
	s.handleDockerDBCreate(rec, grpBReq("POST", "{bad"))
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadRequest {
		t.Errorf("docker create = %d, want 503 or 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleDockerDBCreate(rec, grpBReq("POST", `{"engine":"mariadb"}`))
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadRequest {
		t.Errorf("docker create missing fields = %d, want 503 or 400", rec.Code)
	}
}

func TestGrpB_DockerDBCreateDatabaseValidation(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := grpBReq("POST", "{bad")
	req.SetPathValue("name", "container")
	s.handleDockerDBCreateDatabase(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = grpBReq("POST", `{"user":"u"}`)
	req.SetPathValue("name", "container")
	s.handleDockerDBCreateDatabase(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing db name = %d, want 400", rec.Code)
	}
}

// =============================================================================
// DB Explorer SQL safety branches
// =============================================================================

func TestGrpB_DBExploreTablesInvalidName(t *testing.T) {
	s := testServer()

	// empty db name
	rec := httptest.NewRecorder()
	req := grpBReq("GET", "")
	req.SetPathValue("db", "")
	s.handleDBExploreTables(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty db = %d, want 400", rec.Code)
	}

	// invalid identifier
	rec = httptest.NewRecorder()
	req = grpBReq("GET", "")
	req.SetPathValue("db", "bad name;DROP")
	s.handleDBExploreTables(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid db = %d, want 400", rec.Code)
	}
}

func TestGrpB_DBExploreColumnsInvalidName(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := grpBReq("GET", "")
	req.SetPathValue("db", "bad;name")
	req.SetPathValue("table", "t")
	s.handleDBExploreColumns(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid = %d, want 400", rec.Code)
	}
}

func TestGrpB_DBExploreQuerySafety(t *testing.T) {
	s := testServer()

	mk := func(db, body string) *http.Request {
		req := grpBReq("POST", body)
		req.SetPathValue("db", db)
		return req
	}

	// invalid db identifier
	rec := httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("bad;db", `{"sql":"SELECT 1"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid db = %d, want 400", rec.Code)
	}

	// invalid JSON
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", "{bad"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}

	// missing sql
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", `{"sql":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing sql = %d, want 400", rec.Code)
	}

	// multi-statement -> 403
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", `{"sql":"SELECT 1; DROP TABLE x"}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("multi-statement = %d, want 403", rec.Code)
	}

	// non-read-only -> 403
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", `{"sql":"DELETE FROM users"}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("delete = %d, want 403", rec.Code)
	}

	// INTO OUTFILE -> 403
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", `{"sql":"SELECT * FROM x INTO OUTFILE '/tmp/x'"}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("outfile = %d, want 403", rec.Code)
	}

	// FOR UPDATE -> 403
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", `{"sql":"SELECT * FROM x FOR UPDATE"}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("for update = %d, want 403", rec.Code)
	}

	// LOAD_FILE -> 403
	rec = httptest.NewRecorder()
	s.handleDBExploreQuery(rec, mk("mydb", `{"sql":"SELECT LOAD_FILE('/etc/passwd')"}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("load_file = %d, want 403", rec.Code)
	}
}
