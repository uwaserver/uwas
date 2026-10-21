package php

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// installDeps runs the submitted install task inline and records the
// argument PhpRunInstall received.
type installDeps struct {
	*fakeDeps
	got string
}

func (d *installDeps) TaskSubmit(_, _, _ string, fn func(func(string)) error) *TaskInfo {
	_ = fn(func(string) {})
	return &TaskInfo{}
}
func (d *installDeps) PhpRunInstall(v string) (string, error) { d.got = v; return "", nil }

func postInstall(d *installDeps, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	New(d).Install(rec, httptest.NewRequest("POST", "/api/v1/php/install", strings.NewReader(body)))
	return rec
}

// Issue #115: the installer received the distro name ("Ubuntu 24.04 LTS")
// instead of the PHP version, producing packages like "phpUbuntu 24.04 LTS-cgi".
func TestInstallPassesVersionNotDistro(t *testing.T) {
	d := &installDeps{fakeDeps: &fakeDeps{mgr: twoTenantMgr(), admin: true}}
	if rec := postInstall(d, `{"version":"8.4"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if d.got != "8.4" {
		t.Fatalf("PhpRunInstall got %q, want 8.4", d.got)
	}
}

func TestInstallRejectsEOLVersion(t *testing.T) {
	d := &installDeps{fakeDeps: &fakeDeps{mgr: twoTenantMgr(), admin: true}}
	if rec := postInstall(d, `{"version":"8.1"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if d.got != "" {
		t.Fatalf("installer must not run for EOL version, got %q", d.got)
	}
}
