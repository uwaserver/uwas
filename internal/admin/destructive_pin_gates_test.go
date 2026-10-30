package admin

// F36001/F36002: CONTRIBUTING.md and ARCHITECTURE.md say destructive operations
// require the admin PIN, and the panel applies it to every sibling of these
// routes (database uninstall, backup restore/delete, file delete, user delete).
// Three destructive routes skipped it, so a stolen API key or session - the very
// thing the PIN is a second factor against - could still run them:
//
//   - POST /packages/install {"action":"remove"} purges OS packages, among them
//     MariaDB ("ALL databases will be destroyed!") and Docker, while
//     POST /database/uninstall needs the PIN;
//   - POST /software/{name}/restore wipes a live volume and extracts a backup
//     over it, while POST /backups/restore needs the PIN;
//   - DELETE /software/{name}/backups/{backup} deletes a backup archive, while
//     DELETE /backups/{name} needs the PIN.
//
// The fixtures never reach anything that executes: the package is one UWAS may
// not remove, and the software instance does not exist.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

func pinGateRequest(s *Server, method, path, body, pin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if pin != "" {
		req.Header.Set("X-Pin-Code", pin)
	}
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "a", Username: "admin", Role: auth.RoleAdmin, Enabled: true}))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func pinGateError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	e, _ := body["error"].(string)
	return e
}

func TestDestructiveRoutesRequirePin(t *testing.T) {
	const pin = "123456"
	cases := []struct {
		name, method, path, body string
		// afterPin is the status the route answers once the PIN is accepted
		// (the fixture is chosen so that this is an early, harmless rejection).
		afterPin int
	}{
		{"package removal", "POST", "/api/v1/packages/install", `{"id":"docker-compose","action":"remove"}`, http.StatusForbidden},
		{"software restore", "POST", "/api/v1/software/ghost/restore", `{"backup":"x"}`, http.StatusNotFound},
		{"software backup delete", "DELETE", "/api/v1/software/ghost/backups/x.tar.gz", ``, http.StatusNotFound},
		// F39000: an import runs the uploaded SQL as the database administrator
		// (it can DROP DATABASE); DELETE /database/{name} needs the PIN. The
		// fixture targets the protected "mysql" schema, which the handler itself
		// rejects with 400 before touching anything.
		{"database import", "POST", "/api/v1/database/mysql/import", `SELECT 1;`, http.StatusBadRequest},
		{"docker database import", "POST", "/api/v1/database/docker/c1/databases/mysql/import", `SELECT 1;`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testServer()
			s.config.Global.Admin.PinCode = pin

			rec := pinGateRequest(s, c.method, c.path, c.body, "")
			if rec.Code != http.StatusForbidden || pinGateError(t, rec) != "pin_required" {
				t.Fatalf("without the PIN: %d %s, want 403 pin_required", rec.Code, rec.Body.String())
			}
			rec = pinGateRequest(s, c.method, c.path, c.body, "wrong-pin")
			if rec.Code != http.StatusForbidden || pinGateError(t, rec) != "invalid_pin" {
				t.Fatalf("with a wrong PIN: %d %s, want 403 invalid_pin", rec.Code, rec.Body.String())
			}
			rec = pinGateRequest(s, c.method, c.path, c.body, pin)
			if rec.Code != c.afterPin || strings.Contains(pinGateError(t, rec), "pin") {
				t.Fatalf("with the PIN: %d %s, want %d from the handler itself", rec.Code, rec.Body.String(), c.afterPin)
			}
		})
	}
}

// Controls: package installation stays PIN-free (the setup flow installs
// packages), and nothing is demanded when no PIN is configured.
func TestPackageInstallAndNoPinConfiguredStayOpen(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.PinCode = "123456"
	rec := pinGateRequest(s, "POST", "/api/v1/packages/install", `{"id":"no-such-package","action":"install"}`, "")
	if rec.Code != http.StatusBadRequest || strings.Contains(pinGateError(t, rec), "pin") {
		t.Fatalf("install without PIN: %d %s, want 400 unknown package", rec.Code, rec.Body.String())
	}

	s = testServer()
	rec = pinGateRequest(s, "POST", "/api/v1/packages/install", `{"id":"docker-compose","action":"remove"}`, "")
	if rec.Code != http.StatusForbidden || strings.Contains(pinGateError(t, rec), "pin") {
		t.Fatalf("remove with no PIN configured: %d %s, want the handler's own 403", rec.Code, rec.Body.String())
	}
}
