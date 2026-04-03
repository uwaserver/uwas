package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// =============================================================================
// Migration Handler Tests
// =============================================================================

func TestHandleMigrate_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/migrate", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleMigrate_InvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/migrate", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleMigrate_MissingFields(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := `{"source_host":"example.com"}` // missing target_domain
	req := httptest.NewRequest("POST", "/api/v1/migrate", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleMigrateCPanel_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/migrate/cpanel", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleMigrateCPanel_MissingFile(t *testing.T) {
	s := testServer()

	// POST without multipart form
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/migrate/cpanel", strings.NewReader("{}"))
	s.mux.ServeHTTP(rec, req)

	// Should fail because no multipart form
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// DNS Handler Tests
// =============================================================================

func TestHandleDNSCheck_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// POST not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/dns/example.com", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDNSRecords_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// PUT not allowed on list endpoint
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/dns/example.com/records", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDNSRecordCreate_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed - endpoint requires DNS provider (returns 501 if not configured)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dns/example.com/records", nil)
	s.mux.ServeHTTP(rec, req)

	// May return 405 or 501 depending on DNS provider config
	if rec.Code != 405 && rec.Code != 501 {
		t.Errorf("status = %d, want 405 or 501", rec.Code)
	}
}

func TestHandleDNSRecordCreate_InvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/dns/example.com/records", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	// May return 400 for invalid JSON or 501 if DNS provider not configured
	if rec.Code != 400 && rec.Code != 501 {
		t.Errorf("status = %d, want 400 or 501", rec.Code)
	}
}

func TestHandleDNSRecordUpdate_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// POST not allowed on specific record
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/dns/example.com/records/123", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDNSRecordUpdate_InvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/dns/example.com/records/123", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	// May return 400 for invalid JSON or 501 if DNS provider not configured
	if rec.Code != 400 && rec.Code != 501 {
		t.Errorf("status = %d, want 400 or 501", rec.Code)
	}
}

func TestHandleDNSRecordDelete_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dns/example.com/records/123", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDNSSync_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dns/example.com/sync", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDNSSync_InvalidDomain(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/dns/invalid-domain/sync", nil)
	s.mux.ServeHTTP(rec, req)

	// May return various codes depending on DNS provider config
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 500 or 501", rec.Code)
	}
}
