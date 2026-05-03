package admin

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/migrate"
)

// =============================================================================
// Cloudflare Tests
// =============================================================================

// generateCloudflareID and generateCloudflareToken were removed in v0.2.0 —
// real tunnels use Cloudflare-issued UUIDs and connector tokens from the API.

func TestFetchCloudflareZones(t *testing.T) {
	srv := testServer()

	// Test with invalid token (will fail HTTP request)
	zones, err := srv.fetchCloudflareZones("invalid-token")
	if err == nil {
		t.Error("fetchCloudflareZones with invalid token should return error")
	}
	if zones != nil {
		t.Error("fetchCloudflareZones should return nil zones on error")
	}
}

func TestFetchCloudflareDNSRecords(t *testing.T) {
	srv := testServer()

	// Test with invalid token (will fail HTTP request)
	records, err := srv.fetchCloudflareDNSRecords("invalid-token", "zone-id")
	if err == nil {
		t.Error("fetchCloudflareDNSRecords with invalid token should return error")
	}
	if records != nil {
		t.Error("fetchCloudflareDNSRecords should return nil records on error")
	}
}

func TestPurgeCloudflareCache(t *testing.T) {
	srv := testServer()

	// Test with invalid token (will fail HTTP request)
	err := srv.purgeCloudflareCache("invalid-token", "https://example.com", false)
	if err == nil {
		t.Error("purgeCloudflareCache with invalid token should return error")
	}

	// Test purge everything with invalid token
	err = srv.purgeCloudflareCache("invalid-token", "", true)
	if err == nil {
		t.Error("purgeCloudflareCache everything with invalid token should return error")
	}
}

func TestHandleCloudflareZoneSync(t *testing.T) {
	srv := testServer()

	// Test without zone ID
	req := httptest.NewRequest("POST", "/api/v1/cloudflare/zones//sync", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.handleCloudflareZoneSync(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleCloudflareZoneSync without zone ID: expected %d, got %d", http.StatusBadRequest, w.Code)
	}

	// Test without cloudflare connection
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/cloudflare/zones/zone123/sync", nil)
	req.Header.Set("X-API-Key", "test-key")
	srv.handleCloudflareZoneSync(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleCloudflareZoneSync without connection: expected %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// =============================================================================
// Database Explorer Tests
// =============================================================================

func TestHandleDBExploreTables(t *testing.T) {
	srv := testServer()

	// Test without database name
	req := httptest.NewRequest("GET", "/api/v1/db/explore//tables", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.handleDBExploreTables(w, withAdminContext(req))
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleDBExploreTables without db: expected %d, got %d", http.StatusBadRequest, w.Code)
	}

	// Test with invalid database name
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/db/explore/invalid-db!/tables", nil)
	req.Header.Set("X-API-Key", "test-key")
	srv.handleDBExploreTables(w, withAdminContext(req))
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleDBExploreTables with invalid db: expected %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandleDBExploreColumns(t *testing.T) {
	srv := testServer()

	// Test without database and table name
	req := httptest.NewRequest("GET", "/api/v1/db/explore//tables//columns", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.handleDBExploreColumns(w, withAdminContext(req))
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleDBExploreColumns without db/table: expected %d, got %d", http.StatusBadRequest, w.Code)
	}

	// Test with invalid database name
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/db/explore/invalid-db!/tables/users/columns", nil)
	req.Header.Set("X-API-Key", "test-key")
	srv.handleDBExploreColumns(w, withAdminContext(req))
	if w.Code != http.StatusBadRequest {
		t.Errorf("handleDBExploreColumns with invalid db: expected %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// =============================================================================
// Migration Tests
// =============================================================================

func TestCreateDomainsFromMigration(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "webroot")
	os.MkdirAll(root, 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0"},
			WebRoot: root,
		},
		Domains: []config.Domain{
			{Host: "existing.com", Type: "static", Root: filepath.Join(root, "existing.com"), SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	srv := New(cfg, log, m)

	// Test with various domains
	result := &migrate.CPanelResult{
		Domains: []migrate.CPanelDomain{
			{Domain: "newdomain.com", DocRoot: "public_html"},
			{Domain: "existing.com", DocRoot: "public_html"}, // Already exists
			{Domain: "", DocRoot: "public_html"},             // Empty domain
			{Domain: "unknown", DocRoot: "public_html"},      // Unknown domain
		},
	}

	added := srv.createDomainsFromMigration(result, root)
	if len(added) != 1 {
		t.Errorf("createDomainsFromMigration: expected 1 added, got %d", len(added))
	}
	if len(added) > 0 && added[0] != "newdomain.com" {
		t.Errorf("createDomainsFromMigration: expected newdomain.com, got %s", added[0])
	}

	// Verify domain was added to config
	srv.configMu.RLock()
	domainCount := len(srv.config.Domains)
	srv.configMu.RUnlock()
	if domainCount != 2 {
		t.Errorf("createDomainsFromMigration: expected 2 domains in config, got %d", domainCount)
	}
}

func TestCreateDomainsFromMigrationEmpty(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "webroot")
	os.MkdirAll(root, 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0"},
			WebRoot: root,
		},
		Domains: []config.Domain{},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	srv := New(cfg, log, m)

	// Test with empty domains
	result := &migrate.CPanelResult{
		Domains: []migrate.CPanelDomain{},
	}

	added := srv.createDomainsFromMigration(result, root)
	if len(added) != 0 {
		t.Errorf("createDomainsFromMigration with empty result: expected 0 added, got %d", len(added))
	}
}

func TestSaveUploadedFile(t *testing.T) {
	// Create a test multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("backup", "test.tar.gz")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}
	content := []byte("fake backup content")
	part.Write(content)
	writer.Close()

	// Create a request with the multipart form
	req := httptest.NewRequest("POST", "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Test saveUploadedFile
	tmpPath, header, err := saveUploadedFile(req, "backup")
	if err != nil {
		t.Errorf("saveUploadedFile failed: %v", err)
	}
	if tmpPath == "" {
		t.Error("saveUploadedFile returned empty path")
	}
	if header == nil {
		t.Error("saveUploadedFile returned nil header")
	}
	if header.Filename != "test.tar.gz" {
		t.Errorf("saveUploadedFile filename: expected test.tar.gz, got %s", header.Filename)
	}

	// Cleanup
	if tmpPath != "" {
		os.Remove(tmpPath)
	}
}

func TestSaveUploadedFileMissingField(t *testing.T) {
	// Create a test multipart form without the field
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("other", "value")
	writer.Close()

	// Create a request with the multipart form
	req := httptest.NewRequest("POST", "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Test saveUploadedFile with missing field
	_, _, err := saveUploadedFile(req, "backup")
	if err == nil {
		t.Error("saveUploadedFile with missing field should return error")
	}
}

// =============================================================================
// Recovery Code Tests
// =============================================================================

func TestHandleGenRecoveryCodes(t *testing.T) {
	srv := testServer()

	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-codes", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	srv.handleGenRecoveryCodes(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleGenRecoveryCodes: expected %d, got %d", http.StatusOK, w.Code)
	}

	var resp struct {
		Codes []string `json:"codes"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(resp.Codes) != 8 {
		t.Errorf("handleGenRecoveryCodes: expected 8 codes, got %d", len(resp.Codes))
	}
	if resp.Count != 8 {
		t.Errorf("handleGenRecoveryCodes: expected count 8, got %d", resp.Count)
	}

	// Verify codes were stored
	srv.configMu.RLock()
	storedCodes := srv.config.Global.Admin.RecoveryCodes
	srv.configMu.RUnlock()
	if len(storedCodes) != 8 {
		t.Errorf("handleGenRecoveryCodes: expected 8 stored codes, got %d", len(storedCodes))
	}

	// Verify codes are 8 characters hex
	for i, code := range resp.Codes {
		if len(code) != 8 {
			t.Errorf("handleGenRecoveryCodes: code %d has %d characters, expected 8", i, len(code))
		}
		for _, c := range code {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("handleGenRecoveryCodes: code %d has non-hex character: %c", i, c)
			}
		}
	}
}

func TestHandleUseRecoveryCode(t *testing.T) {
	srv := testServer()

	// First generate some recovery codes
	srv.configMu.Lock()
	srv.config.Global.Admin.RecoveryCodes = []string{
		"abcd1234",
		"efgh5678",
		"ijkl9012",
		"mnop3456",
		"qrst7890",
		"uvwx1234",
		"yzab5678",
		"cdef9012",
	}
	srv.configMu.Unlock()

	// Test with valid code
	body := map[string]string{"code": "abcd1234"}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-code", bytes.NewReader(bodyJSON))
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleUseRecoveryCode(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleUseRecoveryCode with valid code: expected %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("handleUseRecoveryCode: expected status ok, got %s", resp["status"])
	}

	// Verify code was removed
	srv.configMu.RLock()
	remainingCodes := srv.config.Global.Admin.RecoveryCodes
	srv.configMu.RUnlock()
	if len(remainingCodes) != 7 {
		t.Errorf("handleUseRecoveryCode: expected 7 remaining codes, got %d", len(remainingCodes))
	}
	for _, code := range remainingCodes {
		if code == "abcd1234" {
			t.Error("handleUseRecoveryCode: used code should have been removed")
		}
	}
}

func TestHandleUseRecoveryCodeInvalid(t *testing.T) {
	srv := testServer()

	// First generate some recovery codes
	srv.configMu.Lock()
	srv.config.Global.Admin.RecoveryCodes = []string{
		"abcd1234",
		"efgh5678",
	}
	srv.configMu.Unlock()

	// Test with invalid code
	body := map[string]string{"code": "invalid000"}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-code", bytes.NewReader(bodyJSON))
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleUseRecoveryCode(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleUseRecoveryCode with invalid code: expected %d, got %d", http.StatusUnauthorized, w.Code)
	}

	// Verify codes were not modified
	srv.configMu.RLock()
	remainingCodes := srv.config.Global.Admin.RecoveryCodes
	srv.configMu.RUnlock()
	if len(remainingCodes) != 2 {
		t.Errorf("handleUseRecoveryCode: codes should not be modified, expected 2, got %d", len(remainingCodes))
	}
}

func TestHandleUseRecoveryCodeEmpty(t *testing.T) {
	srv := testServer()

	// Test with empty recovery codes list
	srv.configMu.Lock()
	srv.config.Global.Admin.RecoveryCodes = []string{}
	srv.configMu.Unlock()

	body := map[string]string{"code": "anycode1"}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-code", bytes.NewReader(bodyJSON))
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleUseRecoveryCode(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("handleUseRecoveryCode with empty codes: expected %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestHandleUseRecoveryCodeInvalidJSON(t *testing.T) {
	srv := testServer()

	// Test with invalid JSON
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/recovery-code", strings.NewReader("invalid json"))
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleUseRecoveryCode(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("handleUseRecoveryCode with invalid JSON: expected %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// =============================================================================
// Docker DB Tests
// =============================================================================

func TestHandleDockerDBExport(t *testing.T) {
	srv := testServer()

	// Test without database name
	req := httptest.NewRequest("POST", "/api/v1/db/docker//export", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.handleDockerDBExport(w, withAdminContext(req))
	// Returns 400 or 500 depending on docker availability
	if w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
		t.Errorf("handleDockerDBExport without db: expected 400 or 500, got %d", w.Code)
	}
}

func TestHandleDockerDBImport(t *testing.T) {
	srv := testServer()

	// Test without database name
	req := httptest.NewRequest("POST", "/api/v1/db/docker//import", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.handleDockerDBImport(w, withAdminContext(req))
	// Returns 400 or 500 depending on docker availability
	if w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
		t.Errorf("handleDockerDBImport without db: expected 400 or 500, got %d", w.Code)
	}
}

// =============================================================================
// PHP Overrides Tests - Full Implementation
// =============================================================================

func TestPersistDomainPHPOverridesFull(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0"},
			WebRoot: dir,
		},
		Domains: []config.Domain{
			{
				Host: "example.com",
				Type: "php",
				Root: filepath.Join(dir, "example.com"),
				SSL:  config.SSLConfig{Mode: "auto"},
				PHP: config.PHPConfig{
					ConfigOverrides: map[string]string{
						"memory_limit":       "256M",
						"max_execution_time": "60",
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	srv := New(cfg, log, m)
	srv.configPath = dir

	// Skip if phpMgr is nil (will panic otherwise)
	if srv.phpMgr == nil {
		t.Skip("PHP manager not initialized")
	}

	// Test persistDomainPHPOverrides
	srv.persistDomainPHPOverrides("example.com")

	// Verify file was created
	domainFile := filepath.Join(domainsDir, "example.com.yaml")
	if _, err := os.Stat(domainFile); os.IsNotExist(err) {
		t.Error("persistDomainPHPOverrides: domain file should have been created")
	}

	// Test with non-existent domains directory
	srv.configPath = filepath.Join(dir, "nonexistent")
	srv.persistDomainPHPOverrides("example.com")
	// Should not panic even if directory doesn't exist
}

func TestPersistDomainPHPOverridesNoOverrides(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "domains.d"), 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0"},
			WebRoot: dir,
		},
		Domains: []config.Domain{
			{
				Host: "example.com",
				Type: "php",
				Root: filepath.Join(dir, "example.com"),
				SSL:  config.SSLConfig{Mode: "auto"},
				PHP: config.PHPConfig{
					ConfigOverrides: map[string]string{}, // Empty overrides
				},
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	srv := New(cfg, log, m)
	srv.configPath = dir

	// Skip if phpMgr is nil (will panic otherwise)
	if srv.phpMgr == nil {
		t.Skip("PHP manager not initialized")
	}

	// Test persistDomainPHPOverrides with empty overrides
	srv.persistDomainPHPOverrides("example.com")
	// Should not panic with empty overrides
}
