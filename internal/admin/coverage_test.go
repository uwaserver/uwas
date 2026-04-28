package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// --- handleSystem ---

func TestHandleSystem(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify required fields are present
	requiredFields := []string{"go_version", "cpus", "goroutines", "memory_alloc", "version", "os", "arch", "memory_sys", "gc_cycles", "commit"}
	for _, field := range requiredFields {
		if _, ok := body[field]; !ok {
			t.Errorf("missing field %q in system response", field)
		}
	}

	cpus, ok := body["cpus"].(float64)
	if !ok || cpus < 1 {
		t.Errorf("cpus = %v, want >= 1", body["cpus"])
	}

	goroutines, ok := body["goroutines"].(float64)
	if !ok || goroutines < 1 {
		t.Errorf("goroutines = %v, want >= 1", body["goroutines"])
	}

	goVer, ok := body["go_version"].(string)
	if !ok || !strings.HasPrefix(goVer, "go") {
		t.Errorf("go_version = %v, want starts with 'go'", body["go_version"])
	}
}

// --- Backup handlers ---

func testBackupManager(t *testing.T) *backup.BackupManager {
	t.Helper()
	dir := t.TempDir()
	cfg := config.BackupConfig{
		Enabled: true,
		Local:   config.BackupLocalConfig{Path: dir},
		Keep:    3,
	}
	log := logger.New("error", "text")
	return backup.New(cfg, log)
}

func TestSetBackupManager(t *testing.T) {
	s := testServer()
	if s.backupMgr != nil {
		t.Error("backupMgr should be nil initially")
	}

	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)

	if s.backupMgr == nil {
		t.Error("backupMgr should be set after SetBackupManager")
	}
}

func TestHandleBackupListNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleBackupListEmpty(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var backups []backup.BackupInfo
	json.Unmarshal(rec.Body.Bytes(), &backups)
	if len(backups) != 0 {
		t.Errorf("backups count = %d, want 0", len(backups))
	}
}

func TestHandleBackupCreateNoManager(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"provider":"local"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups", body))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleBackupCreateDefaultProvider(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)

	dir := t.TempDir()
	cfgPath := dir + "/uwas.yaml"
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	mgr.SetPaths(cfgPath, "")

	body := strings.NewReader(`{}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups", body))

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBackupCreateBadJSON(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBackupRestoreNoManager(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"name":"test","provider":"local"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups/restore", body))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleBackupRestoreBadJSON(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups/restore", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBackupRestoreMissingName(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	body := strings.NewReader(`{"provider":"local"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups/restore", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBackupRestoreNotFound(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	body := strings.NewReader(`{"name":"nonexistent.tar.gz","provider":"local"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups/restore", body))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandleBackupDeleteNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/backups/test.tar.gz", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleBackupDeleteNotFound(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/backups/nonexistent.tar.gz", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandleBackupScheduleGetNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups/schedule", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleBackupScheduleGetWithManager(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	defer mgr.Stop()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups/schedule", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
}

func TestHandleBackupSchedulePutNoManager(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"interval":"1h"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleBackupSchedulePutBadJSON(t *testing.T) {
	s := testServer()
	s.SetBackupManager(testBackupManager(t))

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBackupSchedulePutEnable(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	defer mgr.Stop()

	body := strings.NewReader(`{"interval":"1h"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["enabled"] != true {
		t.Errorf("enabled = %v, want true", resp["enabled"])
	}
	if resp["interval"] == nil {
		t.Errorf("interval is missing")
	}
	if resp["keep"] == nil {
		t.Errorf("keep is missing")
	}
}

func TestHandleBackupSchedulePutDisable(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	defer mgr.Stop()

	mgr.ScheduleBackup(1 * time.Hour)

	body := strings.NewReader(`{"enabled":false}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["enabled"] != false {
		t.Errorf("enabled = %v, want false", resp["enabled"])
	}
}

func TestHandleBackupSchedulePutInvalidInterval(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	defer mgr.Stop()

	body := strings.NewReader(`{"interval":"notaduration"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBackupSchedulePutTooShort(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	defer mgr.Stop()

	body := strings.NewReader(`{"interval":"30s"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBackupSchedulePutMissingInterval(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	defer mgr.Stop()

	body := strings.NewReader(`{}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestBackupCreateListDeleteFlow(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)

	dir := t.TempDir()
	cfgPath := dir + "/uwas.yaml"
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	mgr.SetPaths(cfgPath, "")

	body := strings.NewReader(`{"provider":"local"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/backups", body))
	if rec.Code != 201 {
		t.Fatalf("create: status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}

	var created backup.BackupInfo
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Name == "" {
		t.Fatal("created backup should have a name")
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups", nil))
	if rec.Code != 200 {
		t.Fatalf("list: status = %d, want 200", rec.Code)
	}

	var backups []backup.BackupInfo
	json.Unmarshal(rec.Body.Bytes(), &backups)
	if len(backups) < 1 {
		t.Fatalf("backups count = %d, want >= 1", len(backups))
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/backups/"+created.Name, nil))
	if rec.Code != 200 {
		t.Fatalf("delete: status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// --- PHP domain handlers ---

func TestPHPDomainsListNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/domains", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainsListEmpty(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/domains", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestPHPDomainAssignNoManager(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"domain":"test.com","version":"8.4"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains", body))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainAssignBadJSON(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPHPDomainAssignMissingDomain(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`{"version":"8.4"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPHPDomainAssignMissingVersion(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`{"domain":"test.com"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPHPDomainAssignVersionNotFound(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`{"domain":"test.com","version":"99.99"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains", body))

	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestPHPDomainUnassignNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/php/domains/test.com", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainUnassignSuccess(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/php/domains/test.com", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestPHPDomainStartNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains/test.com/start", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainStartNotAssigned(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains/nonexistent.com/start", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestPHPDomainStopNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains/test.com/stop", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainStopNotAssigned(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/domains/nonexistent.com/stop", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestPHPDomainConfigGetNoManager(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/domains/test.com/config", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainConfigGetNotFound(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/domains/nonexistent.com/config", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPHPDomainConfigPutNoManager(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"key":"memory_limit","value":"512M"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/php/domains/test.com/config", body))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestPHPDomainConfigPutBadJSON(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`not json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/php/domains/test.com/config", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPHPDomainConfigPutMissingKey(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`{"value":"512M"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/php/domains/test.com/config", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPHPDomainConfigPutNotAssigned(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(logger.New("error", "text")))

	body := strings.NewReader(`{"key":"memory_limit","value":"512M"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/php/domains/nonexistent.com/config", body))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- Auth middleware ---

func TestAuthMiddlewareCORSPreflight(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/v1/domains", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Errorf("CORS OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Error("should set CORS origin for localhost")
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("should set Access-Control-Allow-Methods")
	}
}

func TestAuthMiddlewareHealthIsPublic(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
		Domains: []config.Domain{
			{Host: "test.com", Type: "static"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("health status = %d, want 200 (public)", rec.Code)
	}
}

func TestAuthMiddlewareDashboardIsPublic(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_uwas/dashboard/", nil)
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code == 401 {
		t.Error("dashboard should be accessible without auth")
	}
}

// --- Audit recording ---

func TestBackupCreateRecordsAudit(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)

	dir := t.TempDir()
	cfgPath := dir + "/uwas.yaml"
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	mgr.SetPaths(cfgPath, "")

	body := strings.NewReader(`{"provider":"local"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/backups", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}

	auditRec := httptest.NewRecorder()
	s.mux.ServeHTTP(auditRec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(auditRec.Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Action == "backup.create" && e.Success {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry for backup.create with success=true")
	}
}

func TestRateLimitMap(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	s.recordAuthFailure("10.0.0.1", "")
	s.recordAuthFailure("10.0.0.2", "")

	rl := s.RateLimitMap()
	if len(rl) != 2 {
		t.Errorf("rate limit map size = %d, want 2", len(rl))
	}

	// User rate limit map should be empty when no usernames passed.
	ul := s.UserRateLimitMap()
	if len(ul) != 0 {
		t.Errorf("user rate limit map size = %d, want 0", len(ul))
	}
}

func TestUserRateLimitMap(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	s.recordAuthFailure("10.0.0.1", "admin")
	s.recordAuthFailure("10.0.0.2", "admin")
	s.recordAuthFailure("10.0.0.3", "user1")

	rl := s.RateLimitMap()
	if len(rl) != 3 {
		t.Errorf("IP rate limit map size = %d, want 3", len(rl))
	}

	ul := s.UserRateLimitMap()
	if len(ul) != 2 {
		t.Errorf("user rate limit map size = %d, want 2", len(ul))
	}
}

func TestHealthEndpointFullComponents(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{Host: "a.com", Type: "static"},
			{Host: "b.com", Type: "proxy"},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	s.SetBackupManager(testBackupManager(t))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	checks := body["checks"].(map[string]any)
	if checks["backup"] != "ok" {
		t.Errorf("backup check = %v, want 'ok'", checks["backup"])
	}
	if body["domains"] != float64(2) {
		t.Errorf("domains = %v, want 2", body["domains"])
	}
}

func TestCachePurgeRecordsAudit(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cache/purge", strings.NewReader(`{}`))
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	auditRec := httptest.NewRecorder()
	s.mux.ServeHTTP(auditRec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(auditRec.Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Action == "cache.purge" && !e.Success {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry for cache.purge with success=false")
	}
}

func TestReloadRecordsAuditOnFailure(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/reload", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}

	auditRec := httptest.NewRecorder()
	s.mux.ServeHTTP(auditRec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(auditRec.Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Action == "config.reload" && !e.Success {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry for config.reload with success=false")
	}
}

func TestBackupDeleteRecordsAudit(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// No manager → records audit with failure
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/backups/test.tar.gz", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	auditRec := httptest.NewRecorder()
	s.mux.ServeHTTP(auditRec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(auditRec.Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Action == "backup.delete" && !e.Success {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry for backup.delete with success=false")
	}
}

func TestBackupRestoreRecordsAudit(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// No manager → records audit
	body := strings.NewReader(`{"name":"test","provider":"local"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/backups/restore", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	auditRec := httptest.NewRecorder()
	s.mux.ServeHTTP(auditRec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(auditRec.Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Action == "backup.restore" && !e.Success {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry for backup.restore with success=false")
	}
}

func TestBackupSchedulePutRecordsAudit(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// No manager → records audit
	body := strings.NewReader(`{"interval":"1h"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/backups/schedule", body)
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	auditRec := httptest.NewRecorder()
	s.mux.ServeHTTP(auditRec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(auditRec.Body.Bytes(), &entries)
	found := false
	for _, e := range entries {
		if e.Action == "backup.schedule" && !e.Success {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected audit entry for backup.schedule with success=false")
	}
}
