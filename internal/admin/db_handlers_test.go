package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// =============================================================================
// Database Handler Tests
// =============================================================================

func TestHandleDBUsers_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// POST not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/users", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBChangePassword_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed - endpoint is POST /api/v1/database/users/password
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/database/users/password", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBChangePassword_InvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/users/password", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDBChangePassword_MissingFields(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := `{"user":"testuser"}` // missing password
	req := httptest.NewRequest("POST", "/api/v1/database/users/password", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDBExport_MethodNotAllowed(t *testing.T) {
	s, _ := testServerWithRoot(t)

	// POST not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/testdb/export", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBImport_MethodNotAllowed(t *testing.T) {
	s, _ := testServerWithRoot(t)

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/database/testdb/import", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBDrop_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/database/testdb", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBInstall_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// PUT not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/database/install", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBExploreTables_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// POST not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/explore/testdb/tables", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBExploreColumns_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// POST not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/explore/testdb/tables/users", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBExploreQuery_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/database/explore/testdb/query", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDBExploreQuery_InvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/explore/testdb/query", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDBExploreQuery_MissingSQL(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := `{"limit":100}` // missing sql
	req := httptest.NewRequest("POST", "/api/v1/database/explore/testdb/query", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDBExploreQuery_InvalidDBName(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := `{"sql":"SELECT * FROM users"}`
	req := httptest.NewRequest("POST", "/api/v1/database/explore/invalid-db-name/query", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	// Should fail validation for invalid db name
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
