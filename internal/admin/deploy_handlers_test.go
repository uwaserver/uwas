package admin

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/deploy"
	"github.com/uwaserver/uwas/internal/logger"
)

// =============================================================================
// Deploy Handler Tests
// =============================================================================

func TestHandleDeploy_DeployManagerNotEnabled(t *testing.T) {
	s := testServer()
	// deployMgr is nil by default

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/apps/example.com/deploy", strings.NewReader("{}"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "deploy manager not enabled") {
		t.Errorf("expected error message about deploy manager, got: %s", body)
	}
}

func TestHandleDeploy_InvalidJSON(t *testing.T) {
	s := testServer()
	// Create a deploy manager
	dm := deploy.New(logger.New("error", "text"))
	s.SetDeployManager(dm)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/apps/example.com/deploy", strings.NewReader("invalid json"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDeploy_DomainNotFound(t *testing.T) {
	s := testServer()
	dm := deploy.New(logger.New("error", "text"))
	s.SetDeployManager(dm)

	rec := httptest.NewRecorder()
	body := `{"git_url":"https://github.com/example/repo.git"}`
	req := httptest.NewRequest("POST", "/api/v1/apps/nonexistent.com/deploy", strings.NewReader(body))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeployStatus_DeployManagerNotEnabled(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/example.com/deploy", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleDeployStatus_NoDeploymentFound(t *testing.T) {
	s, _ := testServerWithRoot(t)
	dm := deploy.New(logger.New("error", "text"))
	s.SetDeployManager(dm)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/example.com/deploy", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeployList_DeployManagerNotEnabled(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/deploys", nil)
	s.mux.ServeHTTP(rec, req)

	// Returns empty array, not error
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body []interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Errorf("expected empty array, got: %v", body)
	}
}

func TestHandleDeployWebhook_DeployManagerNotEnabled(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/apps/example.com/webhook", strings.NewReader("{}"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestHandleDeployWebhook_DomainNotFound(t *testing.T) {
	s := testServer()
	dm := deploy.New(logger.New("error", "text"))
	s.SetDeployManager(dm)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/apps/nonexistent.com/webhook", strings.NewReader("{}"))
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleDeployWebhook_MethodNotAllowed(t *testing.T) {
	s, _ := testServerWithRoot(t)
	dm := deploy.New(logger.New("error", "text"))
	s.SetDeployManager(dm)

	// GET not allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/example.com/webhook", nil)
	s.mux.ServeHTTP(rec, req)

	if rec.Code != 405 {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleDeploy_RequestBodyTooLarge(t *testing.T) {
	s := testServer()
	dm := deploy.New(logger.New("error", "text"))
	s.SetDeployManager(dm)

	// Create a body larger than 1MB
	largeBody := make([]byte, 1<<20+100)
	jsonBody, _ := json.Marshal(map[string]interface{}{
		"git_url": "https://github.com/example/repo.git",
		"data":    string(largeBody),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/apps/example.com/deploy", bytes.NewReader(jsonBody))
	s.mux.ServeHTTP(rec, req)

	// Should either fail with 400 or handle gracefully
	if rec.Code != 400 && rec.Code != 413 {
		t.Errorf("status = %d, want 400 or 413", rec.Code)
	}
}
