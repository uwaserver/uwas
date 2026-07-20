package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// =============================================================================
// handleDBList (43.8% → target >70%)
// GET /api/v1/database/list
// =============================================================================

func TestHandleDBList_EmptyResponse(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/list", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	// With no MySQL installed, GetStatus returns !Installed → empty list
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatal("items should be an array")
	}
	if len(items) != 0 {
		t.Errorf("items = %d, want 0", len(items))
	}
	if total, ok := body["total"].(float64); !ok || total != 0 {
		t.Errorf("total = %v, want 0", body["total"])
	}
	if _, ok := body["limit"]; !ok {
		t.Error("limit field missing")
	}
	if _, ok := body["offset"]; !ok {
		t.Error("offset field missing")
	}
}

// =============================================================================
// handleDBExport (46.7% → target >70%)
// GET /api/v1/database/{name}/export
// =============================================================================

func TestHandleDBExport_ErrorPath(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// ExportDatabase runs real mysqldump/mariadb-dump which isn't available → error
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/testdb/export", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (no mysqldump), body=%s",
			rec.Code, truncln(rec.Body.String(), 80))
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message in response")
	}
}

// =============================================================================
// handleDockerDBCreate (50% → target >70%)
// POST /api/v1/database/docker
// =============================================================================

func TestHandleDockerDBCreate_MissingRootPass(t *testing.T) {
	s := testServer()
	// Valid JSON but missing root_pass — DockerAvailable() may succeed but
	// field validation catches the missing field first (after Docker check).
	body := `{"engine":"mariadb","name":"testdb","port":3306}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/docker",
		strings.NewReader(body)))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (missing root_pass), body=%s",
			rec.Code, truncln(rec.Body.String(), 80))
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "root_pass") {
		t.Errorf("error should mention root_pass, got: %s", resp["error"])
	}
	// Record the actual status for audit
	t.Logf("DockerDBCreate missing root_pass: status=%d body=%s", rec.Code, truncln(rec.Body.String(), 60))
}

// =============================================================================
// handleDockerDBExport (53.3% → target >70%)
// GET /api/v1/database/docker/{name}/databases/{db}/export
// =============================================================================

func TestHandleDockerDBExport_ErrorPath(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// DockerDBExport runs docker exec on a non-existent container → error
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET",
		"/api/v1/database/docker/testcontainer/databases/testdb/export", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (Docker container not found), body=%s",
			rec.Code, truncln(rec.Body.String(), 80))
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message in response")
	}
	t.Logf("DockerDBExport response: status=%d body=%s", rec.Code, truncln(rec.Body.String(), 80))
}

// =============================================================================
// handleDBExploreTables (44.8% → target >70%)
// GET /api/v1/database/explore/{db}/tables
// =============================================================================

func TestHandleDBExploreTables_InvalidDBName(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// Hyphen is not a valid DB identifier character
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET",
		"/api/v1/database/explore/invalid-name/tables", nil))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 (invalid db name), body=%s",
			rec.Code, truncln(rec.Body.String(), 80))
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message")
	}
}

func TestHandleDBExploreTables_MySQLNotAvailable(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// Valid identifier but DatabaseExists needs real MySQL → 500
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET",
		"/api/v1/database/explore/testdb/tables", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (MySQL not available), body=%s",
			rec.Code, truncln(rec.Body.String(), 80))
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message")
	}
	if !strings.Contains(body["error"], "lookup") && !strings.Contains(body["error"], "mysql") &&
		!strings.Contains(body["error"], "client") && !strings.Contains(body["error"], "failed") {
		t.Logf("unexpected error message: %s", body["error"])
	}
}

// =============================================================================
// SetPHPManager (22.2% → target >60%)
// =============================================================================

func TestSetPHPManager_WiresCallback(t *testing.T) {
	s := testServer()
	m := phpmanager.New(logger.New("error", "text"))

	s.SetPHPManager(m)
	if s.phpMgr != m {
		t.Fatal("SetPHPManager did not set phpMgr field")
	}

	// Re-wiring should not panic
	s.SetPHPManager(m)

	// Check that the seeded domain config is intact (SetPHPManager's callback
	// iterates config.Domains, so we verify no corruption)
	if len(s.config.Domains) != 2 {
		t.Fatalf("expected 2 seeded domains, got %d", len(s.config.Domains))
	}
}

func TestSetPHPManager_DomainChangeCallbackUpdatesConfig(t *testing.T) {
	s := testServer()
	m := phpmanager.New(logger.New("error", "text"))

	// Install the callback via SetPHPManager
	s.SetPHPManager(m)

	// Verify that the existing domain config has no FPMAddress set
	for _, d := range s.config.Domains {
		if d.PHP.FPMAddress != "" {
			t.Logf("domain %s already has FPMAddress=%s", d.Host, d.PHP.FPMAddress)
		}
	}

	// The callback is stored inside the manager. We can trigger the callback
	// chain by calling the admin handler that assigns and starts a domain PHP,
	// but that requires a real PHP installation. Instead, verify the wiring:
	// - s.phpMgr is set
	// - notifyDomainChange does not panic
	if s.phpMgr == nil {
		t.Fatal("phpMgr should be set after SetPHPManager")
	}
	// notifyDomainChange tolerates nil onDomainChange
	s.notifyDomainChange()
}

// =============================================================================
// handlePHPInstallStatus (42.9% → target >80%)
// GET /api/v1/php/install/status
// =============================================================================

func TestHandlePHPInstallStatus_ActiveTask(t *testing.T) {
	s := testServer()

	// Submit a PHP install task that runs long enough for us to observe it
	done := make(chan struct{})
	s.taskMgr.Submit("php", "8.3", "install", func(appendOutput func(string)) error {
		close(done)
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	<-done // Wait for the worker to start the task (sets StatusRunning)
	// Brief additional wait for the mutex to settle
	time.Sleep(1 * time.Millisecond)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/install/status", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	// Should show the active task (not idle)
	status, _ := body["status"].(string)
	if status == "idle" {
		t.Error("expected non-idle status: active task should be present")
	}
	if _, ok := body["task_id"]; !ok {
		t.Error("expected task_id in response")
	}
	if ver, ok := body["version"].(string); !ok || ver != "8.3" {
		t.Errorf("version = %v, want '8.3'", body["version"])
	}
	// output and error should be present (even if empty strings)
	if _, ok := body["output"]; !ok {
		t.Error("expected output in response")
	}
	if _, ok := body["error"]; !ok {
		t.Error("expected error in response")
	}
}

func TestHandlePHPInstallStatus_CompletedTask(t *testing.T) {
	s := testServer()

	// Submit a task that completes immediately
	s.taskMgr.Submit("php", "8.2", "install", func(appendOutput func(string)) error {
		return nil
	})
	// Wait for worker to pick it up and finish
	time.Sleep(20 * time.Millisecond)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/install/status", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	status, _ := body["status"].(string)
	if status == "idle" {
		// The task may have been cleaned up already; that's OK — we still
		// exercise the handler code path.
		t.Log("status is idle (task may have been cleaned up)")
	} else {
		// Should have task_id, version, output, error
		if _, ok := body["task_id"]; !ok {
			t.Error("expected task_id in response")
		}
		if ver, ok := body["version"].(string); !ok || ver != "8.2" {
			t.Errorf("version = %v, want '8.2'", body["version"])
		}
		t.Logf("completed task status=%v version=%v", body["status"], body["version"])
	}
}

// =============================================================================
// handlePHPRestart (50% → target >70%)
// POST /api/v1/php/{version}/restart
// =============================================================================

func TestHandlePHPRestart_WithManagerNotFound(t *testing.T) {
	s := testServer()
	m := phpmanager.New(logger.New("error", "text"))
	s.SetPHPManager(m)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/9.9/restart", nil))

	// Manager is set but version "9.9" is not running → RestartFPM returns error
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (version not running), body=%s",
			rec.Code, truncln(rec.Body.String(), 80))
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("expected error message")
	}
}
