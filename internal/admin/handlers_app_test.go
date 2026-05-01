package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/appmanager"
	"github.com/uwaserver/uwas/internal/config"
)

func newTestServerWithApp(t *testing.T) *Server {
	t.Helper()
	s := New(&config.Config{}, nil, nil)
	m := appmanager.New(nil)
	m.Register("node.test.com", config.AppConfig{
		Runtime: "node",
		Command: "echo hello",
		Port:    4000,
	}, "/tmp")
	s.appMgr = m
	return s
}

func TestHandleAppList(t *testing.T) {
	s := newTestServerWithApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps", nil)
	s.handleAppList(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp struct {
		Items []appmanager.AppInstance `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	apps := resp.Items
	if len(apps) != 1 {
		t.Errorf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Domain != "node.test.com" {
		t.Errorf("domain = %q", apps[0].Domain)
	}
}

func TestHandleAppListEmpty(t *testing.T) {
	s := New(&config.Config{}, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps", nil)
	s.handleAppList(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleAppGet(t *testing.T) {
	s := newTestServerWithApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/node.test.com", nil)
	req.SetPathValue("domain", "node.test.com")
	s.handleAppGet(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	var app appmanager.AppInstance
	json.Unmarshal(rec.Body.Bytes(), &app)
	if app.Runtime != "node" {
		t.Errorf("runtime = %q", app.Runtime)
	}
}

func TestHandleAppGetNotFound(t *testing.T) {
	s := newTestServerWithApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/nope.com", nil)
	req.SetPathValue("domain", "nope.com")
	s.handleAppGet(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleAppStopNotRunning(t *testing.T) {
	s := newTestServerWithApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/apps/node.test.com/stop", nil)
	req.SetPathValue("domain", "node.test.com")
	s.handleAppStop(rec, req)

	// Should return 409 because app is registered but not running
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestHandleAppStats(t *testing.T) {
	s := newTestServerWithApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/node.test.com/stats", nil)
	req.SetPathValue("domain", "node.test.com")
	s.handleAppStats(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	var stats appmanager.AppStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Domain != "node.test.com" {
		t.Errorf("domain = %q", stats.Domain)
	}
	// App is registered but not started, so Running should be false
	if stats.Running {
		t.Error("expected not running")
	}
}

func TestHandleAppStatsNotFound(t *testing.T) {
	s := newTestServerWithApp(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/nope.com/stats", nil)
	req.SetPathValue("domain", "nope.com")
	s.handleAppStats(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleAppStatsNoManager(t *testing.T) {
	s := New(&config.Config{}, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/apps/test.com/stats", nil)
	req.SetPathValue("domain", "test.com")
	s.handleAppStats(rec, req)

	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestTerminalHandler(t *testing.T) {
	s := New(&config.Config{}, nil, nil)
	h := s.terminalHandler()
	if h == nil {
		t.Fatal("terminalHandler returned nil")
	}

	// Non-WebSocket request should fail
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/terminal", nil)
	h.ServeHTTP(rec, req)
	// On non-Linux: 501, on Linux without WS: 400
	if rec.Code == 200 {
		t.Errorf("expected non-200 for non-WebSocket request, got %d", rec.Code)
	}
}
