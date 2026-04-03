package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// =============================================================================
// Certificate & Recovery Handler Tests
// =============================================================================

func TestHandleCertUpload_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/certs/example.com/upload", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleCertUpload_InvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/certs/example.com/upload", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCertUpload_MissingFields(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := `{"cert":"-----BEGIN CERTIFICATE-----\nMIIDXTCCAkWg...\n-----END CERTIFICATE-----"}` // missing key
	req := httptest.NewRequest("POST", "/api/v1/certs/example.com/upload", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCertUpload_InvalidHostname(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := `{"cert":"test", "key":"test"}`
	req := httptest.NewRequest("POST", "/api/v1/certs/../etc/passwd/upload", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	// Should fail hostname validation (may redirect 307 or return 400)
	if rec.Code != 400 && rec.Code != 307 {
		t.Errorf("status = %d, want 400 or 307", rec.Code)
	}
}

func TestHandleGenRecoveryCodes_MethodNotAllowed(t *testing.T) {
	s := testServer()

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/2fa/recovery-codes", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleGenRecoveryCodes_Success(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes", strings.NewReader("{}"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["codes"] == nil {
		t.Errorf("expected codes in response, got: %v", body)
	}
	if body["count"] == nil {
		t.Errorf("expected count in response, got: %v", body)
	}
}
