package admin

// F65100: renaming a PHP domain left its phpmanager assignment (worker,
// overrides, cgroup, crash monitor) keyed by the old host, so deleting the
// renamed domain stopped nothing and the worker outlived it.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

func renamePHPTestEnv(t *testing.T, hosts ...string) (*Server, *phpmanager.Manager, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	fakeCGI := filepath.Join(dir, "php-cgi")
	if err := os.WriteFile(fakeCGI, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{}
	var ds []config.Domain
	for _, h := range hosts {
		r := filepath.Join(webRoot, h, "public_html")
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
		roots[h] = r
		ds = append(ds, config.Domain{Host: h, Type: "php", Root: r, SSL: config.SSLConfig{Mode: "off"}})
	}
	s := testServerFromConfig(t, &config.Config{Global: config.GlobalConfig{WebRoot: webRoot}, Domains: ds})
	mgr := phpmanager.New(testLogger(t))
	mgr.SetInstallationsForTesting([]phpmanager.PHPInstall{{Version: "8.3.6", Binary: fakeCGI, SAPI: "cgi-fcgi"}})
	s.SetPHPManager(mgr)
	t.Cleanup(func() {
		for _, d := range mgr.GetDomainInstances() {
			_ = mgr.StopDomain(d.Domain)
			mgr.UnassignDomain(d.Domain)
		}
	})
	return s, mgr, roots
}

func renamePHPDo(s *Server, method, url, body string) int {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	return rec.Code
}

func renamePHPInst(m *phpmanager.Manager, host string) *phpmanager.DomainPHP {
	for _, d := range m.GetDomainInstances() {
		if d.Domain == host {
			return &d
		}
	}
	return nil
}

func TestDomainRenameMovesPHPAssignment(t *testing.T) {
	s, mgr, roots := renamePHPTestEnv(t, "old.test")
	inst, err := mgr.AssignDomainWithRoot("old.test", "8.3", roots["old.test"])
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetDomainConfig("old.test", "memory_limit", "512M"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StartDomain("old.test"); err != nil {
		t.Fatal(err)
	}
	s.config.Domains[0].PHP.FPMAddress = inst.ListenAddr

	if code := renamePHPDo(s, http.MethodPut, "/api/v1/domains/old.test", `{"host":"new.test"}`); code != http.StatusOK {
		t.Fatalf("rename status %d", code)
	}
	if old := renamePHPInst(mgr, "old.test"); old != nil {
		t.Fatalf("old.test still assigned after rename: %+v", old)
	}
	nw := renamePHPInst(mgr, "new.test")
	if nw == nil || !nw.Running || nw.ConfigOverrides["memory_limit"] != "512M" {
		t.Fatalf("new.test assignment = %+v, want running with memory_limit=512M", nw)
	}
	if got := s.config.Domains[0].PHP.FPMAddress; got == "" || got != mgr.RunningAddrForDomain("new.test") {
		t.Fatalf("stored fpm_address = %q, want the new worker's %q", got, mgr.RunningAddrForDomain("new.test"))
	}
	if code := renamePHPDo(s, http.MethodDelete, "/api/v1/domains/new.test?confirm=true", ""); code != http.StatusOK {
		t.Fatalf("delete status %d", code)
	}
	if left := mgr.GetDomainInstances(); len(left) != 0 {
		t.Fatalf("PHP assignments outlived the deleted domain: %+v", left)
	}
}

// When the new host cannot take the assignment over, the running site keeps
// its worker under the old key rather than losing it.
func TestDomainRenameKeepsPHPAssignmentWhenMoveFails(t *testing.T) {
	s, mgr, roots := renamePHPTestEnv(t, "a.test")
	s.config.Domains[0].PHP.FPMAddress = "unix:/run/php/a.sock"
	mgr.RegisterExistingDomain("a.test", "8.3", "unix:/run/php/a.sock", roots["a.test"], nil)
	mgr.RegisterExistingDomain("b.test", "8.3", "unix:/run/php/b.sock", "/elsewhere", nil)
	if code := renamePHPDo(s, http.MethodPut, "/api/v1/domains/a.test", `{"host":"b.test"}`); code != http.StatusOK {
		t.Fatalf("rename status %d", code)
	}
	if a := renamePHPInst(mgr, "a.test"); a == nil || !a.Running {
		t.Fatalf("a.test lost its worker on a failed move: %+v", a)
	}
	if b := renamePHPInst(mgr, "b.test"); b == nil || b.ListenAddr != "unix:/run/php/b.sock" {
		t.Fatalf("foreign b.test assignment changed: %+v", b)
	}
	if got := s.config.Domains[0].PHP.FPMAddress; got != "unix:/run/php/a.sock" {
		t.Fatalf("stored fpm_address = %q, want unchanged", got)
	}
}
