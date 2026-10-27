package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// F27700: the dashboard resumes a running PHP task after a page reload. The
// status endpoint must say which kind of task it is, otherwise a running
// update or uninstall is shown as "Installing PHP ...".
func TestPHPInstallStatusReportsTaskAction(t *testing.T) {
	for _, action := range []string{"install", "update", "uninstall"} {
		s := testServer()
		release := make(chan struct{})
		started := make(chan struct{})
		s.taskMgr.Submit("php", "8.3", action, func(func(string)) error {
			close(started)
			<-release
			return nil
		})
		<-started
		rec := httptest.NewRecorder()
		s.handlePHPInstallStatus(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/php/install/status", nil)))
		close(release)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != "running" {
			t.Fatalf("%s: status = %v, want running", action, body["status"])
		}
		if body["action"] != action {
			t.Errorf("%s: action = %v, want %q", action, body["action"], action)
		}
	}
}
