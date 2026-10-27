package admin

// F32800-F32803: host-changing admin actions that left no audit record while
// the README promises "all admin actions" are tracked: every firewall change
// (allow/deny/move/delete/enable/confirm/disable), cron job add/delete,
// database create, PHP-FPM start/stop/restart/config/install and the
// WordPress install. Failures are audited too.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/router"
)

func serve(s *Server, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestFirewallChangesAreAudited(t *testing.T) {
	s := testServer()
	origAllow, origDisable, origEnable := firewallAllowPort, firewallDisable, firewallEnableWithRollback
	defer func() {
		firewallAllowPort, firewallDisable, firewallEnableWithRollback = origAllow, origDisable, origEnable
	}()
	firewallAllowPort = func(port, proto, from string) error {
		if port == "9" {
			return errors.New("ufw failed")
		}
		return nil
	}
	firewallDisable = func() error { return nil }
	firewallEnableWithRollback = func(time.Duration, []string) error { return nil }

	steps := []struct {
		method, path, body string
		wantCode           int
		wantAudit          string
	}{
		{"POST", "/api/v1/firewall/allow", `{"port":"8080","proto":"tcp"}`, 200, "firewall.allow|true"},
		{"POST", "/api/v1/firewall/allow", `{"port":"9"}`, 500, "firewall.allow|false"}, // failure is audited
		{"POST", "/api/v1/firewall/deny", `{"from":"203.0.113.9"}`, 200, "firewall.deny|true"},
		{"POST", "/api/v1/firewall/3/move", `{"direction":"up"}`, 200, "firewall.move|true"},
		{"DELETE", "/api/v1/firewall/3", ``, 200, "firewall.delete|true"},
		{"POST", "/api/v1/firewall/enable", ``, 200, "firewall.enable|true"},
		{"POST", "/api/v1/firewall/confirm", ``, 200, "firewall.confirm|true"},
		{"POST", "/api/v1/firewall/disable", ``, 200, "firewall.disable|true"},
	}
	for _, c := range steps {
		rec := serve(s, c.method, c.path, c.body)
		t.Logf("%s %s -> %d", c.method, c.path, rec.Code)
		if rec.Code != c.wantCode {
			t.Fatalf("%s %s = %d, want %d (%s)", c.method, c.path, rec.Code, c.wantCode, rec.Body.String())
		}
		ok := hasAudit(s, c.wantAudit)
		t.Logf("EXPECTED: audit %q\nACTUAL: %v", c.wantAudit, auditActions(s))
		if !ok {
			t.Fatalf("PROBLEM CONFIRMED: %s %s left no audit record %q", c.method, c.path, c.wantAudit)
		}
	}
}

func TestDatabaseCreateIsAudited(t *testing.T) {
	s := testServer()
	rec := serve(s, "POST", "/api/v1/database/create", `{"name":"shop","user":"shop","password":"p"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("EXPECTED: audit database.create|true\nACTUAL: %v", auditActions(s))
	if !hasAudit(s, "database.create|true") {
		t.Fatal("PROBLEM CONFIRMED: database create left no audit record")
	}
}

// fakeCrontab puts a stub `crontab` first on PATH so the cron handlers run
// without touching the host's real crontab. It uses only shell builtins.
func fakeCrontab(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"-l\" ]; then exit 0; fi\nwhile IFS= read -r line; do :; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "crontab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestCronChangesAreAudited(t *testing.T) {
	fakeCrontab(t)
	s := testServer()

	rec := serve(s, "POST", "/api/v1/cron", `{"schedule":"*/5 * * * *","command":"/bin/true","domain":"example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cron add = %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("EXPECTED: audit cron.add|true\nACTUAL: %v", auditActions(s))
	if !hasAudit(s, "cron.add|true") {
		t.Fatal("PROBLEM CONFIRMED: cron add left no audit record")
	}

	// Rejected jobs (shell metacharacters) are audited as failures.
	serve(s, "POST", "/api/v1/cron", `{"schedule":"* * * * *","command":"/bin/true; rm -rf /"}`)
	if !hasAudit(s, "cron.add|false") {
		t.Fatalf("PROBLEM CONFIRMED: rejected cron add left no audit record: %v", auditActions(s))
	}

	rec = serve(s, "DELETE", "/api/v1/cron", `{"schedule":"*/5 * * * *","command":"/bin/true"}`)
	t.Logf("cron delete -> %d %s", rec.Code, rec.Body.String())
	if !hasAudit(s, "cron.delete|true") && !hasAudit(s, "cron.delete|false") {
		t.Fatalf("PROBLEM CONFIRMED: cron delete left no audit record: %v", auditActions(s))
	}
}

func TestUnknownDomainTrackerChangesAreAudited(t *testing.T) {
	s := testServer()
	s.SetUnknownHostTracker(router.NewUnknownHostTracker())
	for _, c := range []struct{ method, path, want string }{
		{"POST", "/api/v1/unknown-domains/evil.example/block", "unknown_domain.block|true"},
		{"POST", "/api/v1/unknown-domains/evil.example/unblock", "unknown_domain.unblock|true"},
		{"DELETE", "/api/v1/unknown-domains/evil.example", "unknown_domain.dismiss|true"},
	} {
		rec := serve(s, c.method, c.path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s", c.method, c.path, rec.Code, rec.Body.String())
		}
		t.Logf("EXPECTED: audit %q\nACTUAL: %v", c.want, auditActions(s))
		if !hasAudit(s, c.want) {
			t.Fatalf("PROBLEM CONFIRMED: %s %s left no audit record %q", c.method, c.path, c.want)
		}
	}
}
