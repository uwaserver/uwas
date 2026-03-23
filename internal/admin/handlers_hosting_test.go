package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWPInstallStatusIdle(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/install/status", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "idle" {
		t.Errorf("status = %v, want idle", body["status"])
	}
}

func TestWPInstallMissingDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/install", strings.NewReader(`{}`))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCronListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/cron", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCronAddMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cron", strings.NewReader(`{"schedule":"* * * * *"}`))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallStatusEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/firewall", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["backend"] == nil {
		t.Error("backend should be present")
	}
}

func TestFirewallAllowMissingPort(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/firewall/allow", strings.NewReader(`{}`))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFileListUnknownDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/nonexistent.com/list", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDiskUsageUnknownDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/nonexistent.com/disk-usage", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUpdateCheckEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/system/update-check", nil)
	s.mux.ServeHTTP(rec, req)

	// May fail due to network, but should not panic
	if rec.Code != 200 && rec.Code != 500 {
		t.Fatalf("status = %d, want 200 or 500", rec.Code)
	}
}

func TestSSHKeyListUnknownDomain(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users/nonexistent.com/ssh-keys", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	// Should return empty array
	var keys []string
	json.Unmarshal(rec.Body.Bytes(), &keys)
	if len(keys) != 0 {
		t.Errorf("expected empty keys, got %d", len(keys))
	}
}

func TestSSHKeyAddInvalidKey(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/users/test.com/ssh-keys", strings.NewReader(`{"public_key":"not-a-key"}`))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUserListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}
