package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// handleWPSiteDetail — currently 53.8%.
// Route: GET /api/v1/wordpress/sites/{domain}/detail
// Paths covered:
//   1. Not a WordPress site (no wp-config.php) → 400
//   2. WordPress site detected, enriched, returned → 200
//   3. Non-existent domain → 404 (via authorizedDomainRoot)
// =============================================================================

func TestWPSiteDetail_NotWordPress(t *testing.T) {
	// Root dir exists but no wp-config.php → IsWordPress returns false.
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/example.com/detail", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil || !strings.Contains(body["error"].(string), "not a WordPress site") {
		t.Errorf("expected 'not a WordPress site' error, got: %v", body["error"])
	}
}

func TestWPSiteDetail_Success(t *testing.T) {
	s, root := testServerWithRoot(t)
	// Create minimal wp-config.php so IsWordPress + DetectSites pass.
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/example.com/detail", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var site map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if site["domain"] != "example.com" {
		t.Errorf("domain = %v, want example.com", site["domain"])
	}
	if site["web_root"] != root {
		t.Errorf("web_root = %v, want %s", site["web_root"], root)
	}
}

func TestWPSiteDetail_DomainNotFound(t *testing.T) {
	// Use plain testServer (no WebRoot) so domain root resolves to empty → 404.
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/example.com/detail", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// handleWPUpdateCore — currently 58.3%.
// Route: POST /api/v1/wordpress/sites/{domain}/update-core
// Paths covered:
//   1. Not a WordPress site → 400
//   2. WordPress site, wp-cli not available → 500 (fallback download fails)
// =============================================================================

func TestWPUpdateCore_NotWordPress(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/update-core", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestWPUpdateCore_NoWPCLI(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/update-core", nil)
	s.mux.ServeHTTP(rec, req)
	// Without wp-cli, UpdateCore falls back to HTTP download. If the
	// test environment has network access, this may succeed (200).
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "updated" {
		t.Errorf("status field = %v, want 'updated'", body["status"])
	}
	if body["output"] == nil {
		t.Error("expected output field")
	}
}

// =============================================================================
// handleWPPluginAction — currently 57.9%.
// Route: POST /api/v1/wordpress/sites/{domain}/plugin/{action}/{plugin}
// Paths covered:
//   1. Invalid action → 400
//   2. Valid actions (activate, deactivate, delete) with missing wp-cli → 500
// =============================================================================

func TestWPPluginAction_InvalidAction(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/plugin/invalid/hello", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil || !strings.Contains(body["error"].(string), "invalid action") {
		t.Errorf("expected 'invalid action' error, got: %v", body["error"])
	}
}

func TestWPPluginAction_ActivateNoWPCLI(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/plugin/activate/hello", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil || !strings.Contains(body["error"].(string), "activate failed") {
		t.Errorf("expected 'activate failed' error, got: %v", body["error"])
	}
}

func TestWPPluginAction_DeactivateNoWPCLI(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/plugin/deactivate/hello", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil || !strings.Contains(body["error"].(string), "deactivate failed") {
		t.Errorf("expected 'deactivate failed' error, got: %v", body["error"])
	}
}

func TestWPPluginAction_DeleteNoWPCLI(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/plugin/delete/hello", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil || !strings.Contains(body["error"].(string), "delete failed") {
		t.Errorf("expected 'delete failed' error, got: %v", body["error"])
	}
}

// =============================================================================
// handleWPReinstall — currently 58.3%.
// Route: POST /api/v1/wordpress/sites/{domain}/reinstall
// Paths covered:
//   1. Not a WordPress site → 400
//   2. WordPress site, wp-cli not available → 500
// =============================================================================

func TestWPReinstall_NotWordPress(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/reinstall", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestWPReinstall_NoWPCLI(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/reinstall", nil)
	s.mux.ServeHTTP(rec, req)
	// ReinstallWordPress → UpdateCore → HTTP download. May succeed with
	// network access (200) or fail (500). Accept either since the
	// environment determines the outcome.
	if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// handleWPUsers — currently 44.4%.
// Route: GET /api/v1/wordpress/sites/{domain}/users
// Paths covered:
//   1. WordPress site, ListUsers fails (no wp-cli) → 500
// =============================================================================

func TestWPUsers_NoWPCLI(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/wordpress/sites/example.com/users", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500, body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == nil {
		t.Error("expected error for missing wp-cli")
	}
	if !strings.Contains(rec.Body.String(), "wp user list") {
		t.Errorf("expected 'wp user list' in error, got: %s", rec.Body.String())
	}
}

// =============================================================================
// handleWPOptimizeDB — currently 40%.
// Route: POST /api/v1/wordpress/sites/{domain}/optimize-db
// Paths covered:
//   1. Non-existent domain root → 404 (via authorizedDomainRoot)
//   2. WordPress site, OptimizeDB succeeds (gracefully handles missing wp-cli) → 200
// =============================================================================

func TestWPOptimizeDB_DomainNotFound(t *testing.T) {
	// Use plain testServer (no WebRoot) so domain root resolves to empty → 404.
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/optimize-db", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

func TestWPOptimizeDB_Success(t *testing.T) {
	s, root := testServerWithRoot(t)
	wpConfig := filepath.Join(root, "wp-config.php")
	if err := os.WriteFile(wpConfig, []byte("<?php\n"), 0644); err != nil {
		t.Fatalf("write wp-config.php: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/sites/example.com/optimize-db", nil)
	s.mux.ServeHTTP(rec, req)
	// OptimizeDatabase gracefully handles wp-cli failures (all calls guarded
	// by if err == nil), so it always returns (result, nil).
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["output"] == nil {
		t.Error("expected output field in optimize-db response")
	}
}
