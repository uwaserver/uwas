package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// F63002: a backup request naming a provider that does not exist, or asking to
// restore a per-domain archive, is the caller's mistake (400), not a 500; and a
// bad provider on create must not fire a backup.failed webhook for a backup
// that was never attempted.
func TestBackupCallerErrorsAreNot500(t *testing.T) {
	s := testServer()
	mgr := testBackupManager(t)
	s.SetBackupManager(mgr)
	dir := t.TempDir()
	cfgPath := dir + "/uwas.yaml"
	if err := os.WriteFile(cfgPath, []byte("global: {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr.SetPaths(cfgPath, "")

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"create unknown provider", "POST", "/api/v1/backups", `{"provider":"nope"}`, 400},
		{"restore unknown provider", "POST", "/api/v1/backups/restore", `{"name":"x.tar.gz","provider":"nope"}`, 400},
		{"restore domain archive", "POST", "/api/v1/backups/restore", `{"name":"uwas-domain-a.tar.gz","provider":"local"}`, 400},
		{"delete unknown provider", "DELETE", "/api/v1/backups/x.tar.gz?provider=nope", "", 400},
		{"restore missing archive", "POST", "/api/v1/backups/restore", `{"name":"missing.tar.gz","provider":"local"}`, 404},
		{"delete missing archive", "DELETE", "/api/v1/backups/missing.tar.gz", "", 404},
		// Control: a valid create still succeeds.
		{"create ok", "POST", "/api/v1/backups", `{"provider":"local"}`, 201},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var body *strings.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(c.method, c.path, body)
			s.mux.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("EXPECTED: %d; ACTUAL: %d (%s)", c.want, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
	_ = http.StatusOK
}
