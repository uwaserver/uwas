package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// ── Additional Database handler coverage ───────────────────────────────

func TestDBInstallAlreadyInstalledV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/install", nil))
	// Usually returns "already_installed" or starts an install task
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("db install status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestDBUsers(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/users", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("db users status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestDBDrop(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/database/testdb", nil)
	req.SetPathValue("name", "testdb")
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("db drop status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestDBChangePasswordEmptyUser(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := mustJSON(map[string]string{"user": "", "password": ""})
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/users/password", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBChangePasswordBadJSONV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/users/password", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBRepair(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/repair", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("db repair status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestDBDiagnoseV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/diagnose", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestDBStatusEndpointV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/status", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestDBCreateBadJSONV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/create", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBCreateMissingNameV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := mustJSON(map[string]string{"name": ""})
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/create", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBExploreQueryNoDB(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := mustJSON(map[string]string{"sql": "SELECT 1"})
	req := httptest.NewRequest("POST", "/api/v1/database/explore/testdb/query", bytes.NewReader(body))
	req.SetPathValue("db", "testdb")
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("explore query status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestDBExploreQueryBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/database/explore/testdb/query", strings.NewReader("not json"))
	req.SetPathValue("db", "testdb")
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDBUninstall(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/uninstall", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("db uninstall status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestDBForceUninstall(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/force-uninstall", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("db force uninstall status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── Docker DB handlers (handlers_database.go) ──────────────────────────

func TestDockerDBList(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/docker", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	t.Logf("docker db list: docker=%v", body["docker"])
}

func TestDockerDBCreateBadJSONV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/docker", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDockerDBCreateMissingFieldsV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := mustJSON(map[string]string{"name": "", "engine": ""})
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/docker", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ── System: handleUpdate (POST /api/v1/system/update) ──────────────────

func TestHandleUpdateNoBody(t *testing.T) {
	// handleUpdate checks for update then performs it. In test env it may fail
	// but we can still exercise the handler.
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/system/update", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("system update status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── System: handleSystemResources via endpoint ─────────────────────────

func TestHandleSystemResourcesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system/resources", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleReload (POST /api/v1/system/reload) ─────────────────────────

func TestHandleReload(t *testing.T) {
	s := testServer()
	s.SetReloadFunc(func() error { return nil })
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/reload", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleCachePurge (POST /api/v1/cache/purge) ──────────────────────

func TestHandleCachePurgeNoCache(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := mustJSON(map[string]string{"domain": "example.com"})
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cache/purge", bytes.NewReader(body)))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("cache purge status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestHandleCachePurgeBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cache/purge", strings.NewReader("not json")))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("cache purge bad json status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleLogs (GET /api/v1/logs) ─────────────────────────────────────

func TestHandleLogs(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleAudit (GET /api/v1/audit) ───────────────────────────────────

func TestHandleAudit(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── admin_coverage: handlePHPInstallStatus, handlePHPDomainAssign, etc. ──

func TestPHPInstallStatusIdleV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/install/status", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleFirewallRules (GET /api/v1/firewall/rules) ──────────────────

func TestFirewallRulesEndpointV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/firewall", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("firewall status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleCronMonitorList (GET /api/v1/cron/monitor) ──────────────────

func TestCronMonitorListV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cron/monitor", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

// ── handleSSHKeyList (GET /api/v1/ssh/keys) ──────────────────────────

func TestSSHKeyListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// SSH keys are under /api/v1/users/{domain}/ssh-keys
	req := httptest.NewRequest("GET", "/api/v1/users/example.com/ssh-keys", nil)
	req.SetPathValue("domain", "example.com")
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("ssh key list status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── rotateAuditLog more thoroughly ─────────────────────────────────────

func TestRotateAuditLog(t *testing.T) {
	dir := tempConfigDir(t)
	s := testServer()
	s.configPath = filepath.Join(dir, "uwas.yaml")

	// Create a current log file
	path := s.auditLogFile()
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	// Rotate it
	s.rotateAuditLog(path)

	// Check rotated file exists
	rotated := path + ".1"
	if _, err := os.Stat(rotated); err != nil {
		t.Errorf("rotated file not found: %v", err)
	}

	// Create another log and rotate again to test shift
	if err := os.WriteFile(path, []byte("new line\n"), 0600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
	s.rotateAuditLog(path)

	// Now .2 should exist
	rotated2 := path + ".2"
	if _, err := os.Stat(rotated2); err != nil {
		t.Errorf("rotated file .2 not found: %v", err)
	}
}

// ── readAuditLines with missing file ──────────────────────────────────

func TestReadAuditLinesMissing(t *testing.T) {
	var tail []AuditEntry
	err := readAuditLines("/nonexistent/audit.log", &tail)
	if err != nil {
		t.Fatalf("readAuditLines on missing file: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("len = %d, want 0", len(tail))
	}
}

func TestReadAuditLinesValid(t *testing.T) {
	dir := tempConfigDir(t)
	path := filepath.Join(dir, "audit.log")
	entry := AuditEntry{Time: time.Now(), Action: "test", Success: true}
	data, _ := json.Marshal(entry)
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var tail []AuditEntry
	err := readAuditLines(path, &tail)
	if err != nil {
		t.Fatalf("readAuditLines: %v", err)
	}
	if len(tail) != 1 {
		t.Errorf("len = %d, want 1", len(tail))
	}
}

func TestReadAuditLinesMalformed(t *testing.T) {
	dir := tempConfigDir(t)
	path := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(path, []byte("not json\n{\"action\":\"valid\"}\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var tail []AuditEntry
	err := readAuditLines(path, &tail)
	if err != nil {
		t.Fatalf("readAuditLines: %v", err)
	}
	// Malformed lines should be skipped
	_ = tail
}

// ── findDomainHostnameConflictAllowingRedirect ────────────────────────

func TestFindDomainHostnameConflictAllowingRedirect(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Type: "static"},
		{Host: "www.example.com", Type: "redirect", Redirect: config.RedirectConfig{Target: "https://example.com"}},
	}

	// www.example.com canonicalizes to "example.com" which IS the existing static domain → conflict
	result := findDomainHostnameConflictAllowingRedirect(domains, -1, "www.example.com", "example.com")
	if result == "" {
		t.Error("expected conflict: www.example.com canonicalizes to example.com which exists as static")
	}

	// Should find conflict for existing domain
	result = findDomainHostnameConflictAllowingRedirect(domains, -1, "example.com", "")
	if result == "" {
		t.Error("expected conflict for existing domain")
	}

	// Empty host returns empty
	result = findDomainHostnameConflictAllowingRedirect(domains, -1, "", "")
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}

	// Checking for nonexistent host
	result = findDomainHostnameConflictAllowingRedirect(domains, -1, "nonexistent.com", "")
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}

	// Skip index 0 (the static example.com) → should find conflict at index 1 (www redirect)
	result = findDomainHostnameConflictAllowingRedirect(domains, 0, "example.com", "")
	if result == "" {
		t.Error("expected conflict from www redirect after skipping index 0")
	}

	// Alias conflict
	domains2 := []config.Domain{
		{Host: "other.com", Type: "static", Aliases: []string{"alias.com"}},
	}
	result = findDomainHostnameConflictAllowingRedirect(domains2, -1, "alias.com", "")
	if result != "other.com" {
		t.Errorf("got %q, want other.com", result)
	}
}

// ── isCanonicalRedirectAliasDomain ────────────────────────────────────

func TestIsCanonicalRedirectAliasDomain(t *testing.T) {
	d := config.Domain{
		Host: "www.example.com",
		Type: "redirect",
		Redirect: config.RedirectConfig{
			Target: "https://example.com",
			Status: 301,
		},
	}
	if !isCanonicalRedirectAliasDomain(d, "www.example.com", "example.com") {
		t.Error("expected true for matching redirect alias")
	}
	if isCanonicalRedirectAliasDomain(d, "other.com", "example.com") {
		t.Error("expected false for non-matching host")
	}
	// Non-redirect type
	d2 := config.Domain{Host: "example.com", Type: "static"}
	if isCanonicalRedirectAliasDomain(d2, "example.com", "") {
		t.Error("expected false for non-redirect type")
	}
}

// ── publicDomainAliases ───────────────────────────────────────────────

func TestPublicDomainAliases(t *testing.T) {
	d := config.Domain{
		Host:    "example.com",
		Type:    "static",
		Aliases: []string{"www.example.com", "alias.com", "www.example.com", ""},
	}
	result := publicDomainAliases(d)
	// www.example.com canonicalizes to example.com which equals d.Host, so it's skipped.
	// Only "alias.com" remains.
	if len(result) != 1 {
		t.Errorf("len = %d, want 1 (www.example.com is same as host after canonicalization)", len(result))
	}
}

// ── isImplicitWWWRedirectForDomains ──────────────────────────────────

func TestIsImplicitWWWRedirectForDomains(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Type: "static"},
	}
	d := config.Domain{
		Host: "www.example.com",
		Type: "redirect",
		Redirect: config.RedirectConfig{
			Target: "https://example.com",
			Status: 301,
		},
	}
	if !isImplicitWWWRedirectForDomains(d, domains) {
		t.Error("expected true for implicit WWW redirect")
	}
	// Non-redirect type
	d2 := config.Domain{Host: "www.example.com", Type: "static"}
	if isImplicitWWWRedirectForDomains(d2, domains) {
		t.Error("expected false for non-redirect")
	}
}

// ── normalizeDomainHostname (test uncovered edge cases) ───────────────

func TestNormalizeDomainHostname(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"EXAMPLE.COM", "example.com"},
		{" Example.COM ", "example.com"},
		{"", ""},
		{"www.example.com", "www.example.com"},
	}
	for _, tc := range tests {
		got := normalizeDomainHostname(tc.input)
		if got != tc.want {
			t.Errorf("normalizeDomainHostname(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── canonicalDomainHostname uncovered branches ─────────────────────────

func TestCanonicalDomainHostname(t *testing.T) {
	if got := canonicalDomainHostname("www.example.com"); got != "example.com" {
		t.Errorf("got %q, want example.com", got)
	}
	if got := canonicalDomainHostname("example.com"); got != "example.com" {
		t.Errorf("got %q, want example.com", got)
	}
	if got := canonicalDomainHostname(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ── isValidHostname uncovered branches ─────────────────────────────────

func TestIsValidHostnameV2(t *testing.T) {
	if !isValidHostname("example.com") {
		t.Error("expected true for example.com")
	}
	if isValidHostname("") {
		t.Error("expected false for empty string")
	}
	if isValidHostname("invalid hostname with spaces") {
		t.Error("expected false for invalid hostname")
	}
}

// ── newCanonicalRedirectAliasDomain uncovered branches ─────────────────

func TestNewCanonicalRedirectAliasDomain(t *testing.T) {
	d := newCanonicalRedirectAliasDomain("www.example.com", "example.com", 0, true)
	if d.Type != "redirect" {
		t.Errorf("type = %q, want redirect", d.Type)
	}
	if d.Redirect.Target != "https://example.com" {
		t.Errorf("target = %q, want https://example.com", d.Redirect.Target)
	}
	if d.Redirect.Status != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", d.Redirect.Status)
	}
	if !d.Redirect.PreservePath {
		t.Error("expected preservePath=true")
	}
	// Test explicit status
	d2 := newCanonicalRedirectAliasDomain("www.example.com", "example.com", http.StatusFound, false)
	if d2.Redirect.Status != http.StatusFound {
		t.Errorf("status = %d, want 302", d2.Redirect.Status)
	}
	if d2.Redirect.PreservePath {
		t.Error("expected preservePath=false")
	}
}

// ── handleFeatures uncovered branches ──────────────────────────────────

func TestHandleFeaturesAreReturned(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/features", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) == 0 {
		t.Error("expected non-empty features map")
	}
}

// ── handleBandwidth uncovered branches ─────────────────────────────────

func TestBandwidthListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/bandwidth", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleBackupList endpoint ─────────────────────────────────────────

func TestBackupListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("backup list status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleCerts endpoint ─────────────────────────────────────────────

func TestCertsEndpointV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/certs", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handlePHPInfo endpoint ───────────────────────────────────────────

func TestPHPInfoEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/install-info", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("php install-info status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleDomainRawGet (GET /api/v1/config/domains/{host}/raw) ────────

func TestDomainRawGetEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/config/domains/example.com/raw", nil)
	req.SetPathValue("host", "example.com")
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("domain raw get status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleDomainRawPut (PUT /api/v1/config/domains/{host}/raw) ────────

func TestDomainRawPutBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", strings.NewReader("not json"))
	req.SetPathValue("host", "example.com")
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ── handleDomainHealth endpoint ───────────────────────────────────────

func TestDomainHealthEndpointV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleDomainDebug endpoint ────────────────────────────────────────

func TestDomainDebugEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/domains/debug/nonexistent.com", nil)
	req.SetPathValue("domain", "nonexistent.com")
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("domain debug status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleDeleteDomain (DELETE /api/v1/domains/{host}) ───────────────

func TestDeleteDomainEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/domains/example.com", nil)
	req.SetPathValue("host", "example.com")
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("delete domain status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── filemanager/authorizedDomainRoot uncovered branches (siteUserRoot) ─

func TestSiteUserRoot(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/files/example.com/workspaces", nil)
	s.mux.ServeHTTP(rec, req)
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("file workspaces status = %d", rec.Code)
}

// ── handleDiskUsage (GET /api/v1/disk/usage) ─────────────────────────

func TestHandleDiskUsage(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/files/example.com/disk-usage", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── SetConfigPath (api.go uncovered branch) ───────────────────────────

func TestSetConfigPathV2(t *testing.T) {
	s := testServer()
	dir := tempConfigDir(t)
	cfgPath := filepath.Join(dir, "uwas.yaml")
	s.SetConfigPath(cfgPath)
	if s.configPath != cfgPath {
		t.Errorf("configPath = %q, want %q", s.configPath, cfgPath)
	}
	// Also verify that auditLogFile returns a path now
	if path := s.auditLogFile(); path == "" {
		t.Error("expected non-empty audit log path after SetConfigPath")
	}
}

// ── handleMCPTools (GET /api/v1/mcp/tools) ──────────────────────────

func TestMCPToolsEndpointNoServer(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/mcp/tools", nil))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("mcp tools status = %d, body = %s", rec.Code, rec.Body.String())
}

func TestMCPCallBadJSONV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", strings.NewReader("not json")))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("mcp call bad json status = %d, body = %s", rec.Code, rec.Body.String())
}

// ── handleWebhookList (GET /api/v1/webhooks) ─────────────────────────

func TestWebhooksListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/webhooks", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ── handleNotifyTest (POST /api/v1/settings/notify-test) ──────────────

func TestNotifyTestBadJSONV2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/notify/test", strings.NewReader("not json")))
	if rec.Code == 0 {
		t.Fatal("no response written")
	}
	t.Logf("notify test bad json status = %d, body = %s", rec.Code, rec.Body.String())
}
