package php

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// lifecycleDeps runs submitted tasks inline and records what the seams saw.
type lifecycleDeps struct {
	*fakeDeps
	updated   []string
	removed   []string
	purge     bool
	taskNames []string
	active    *TaskInfo
}

func (d *lifecycleDeps) TaskActive() *TaskInfo { return d.active }
func (d *lifecycleDeps) TaskSubmit(_, name, _ string, fn func(func(string)) error) *TaskInfo {
	d.taskNames = append(d.taskNames, name)
	_ = fn(func(string) {})
	return &TaskInfo{ID: "t1"}
}
func (d *lifecycleDeps) PhpRunUpdate(v string) (string, error) {
	d.updated = append(d.updated, v)
	return "", nil
}
func (d *lifecycleDeps) PhpRunUninstall(v string, purge bool) (string, error) {
	d.removed = append(d.removed, v)
	d.purge = purge
	return "", nil
}

func lifecycleMgr(domainOn83 bool) *phpmanager.Manager {
	m := phpmanager.New(logger.New("error", "text"))
	m.SetInstallationsForTesting([]phpmanager.PHPInstall{
		{Version: "8.3.6", Binary: "/usr/bin/php-fpm8.3", SAPI: "fpm-fcgi", Advisory: "vulnerable"},
		{Version: "8.4.27", Binary: "/usr/bin/php-fpm8.4", SAPI: "fpm-fcgi"},
		{Version: "8.1.30", Binary: "/usr/bin/php-fpm8.1", SAPI: "fpm-fcgi", EOL: true},
	})
	if domainOn83 {
		m.RegisterExistingDomain("site.test", "8.3", "unix:/run/php/a.sock", "/var/www/a", nil)
	}
	return m
}

func newLifecycle(domainOn83 bool) *lifecycleDeps {
	return &lifecycleDeps{fakeDeps: &fakeDeps{mgr: lifecycleMgr(domainOn83), admin: true}}
}

func post(h func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/x", strings.NewReader(body)))
	return rec
}

func TestUpdateSingleVersion(t *testing.T) {
	d := newLifecycle(false)
	rec := post(New(d).Update, `{"version":"8.3"}`)
	if rec.Code != 200 || len(d.updated) != 1 || d.updated[0] != "8.3" {
		t.Fatalf("code=%d updated=%v body=%s", rec.Code, d.updated, rec.Body)
	}
}

func TestUpdateAllSkipsEOL(t *testing.T) {
	d := newLifecycle(false)
	if rec := post(New(d).Update, `{"version":"all"}`); rec.Code != 200 {
		t.Fatalf("code=%d %s", rec.Code, rec.Body)
	}
	got := strings.Join(d.updated, ",")
	if !strings.Contains(got, "8.3") || !strings.Contains(got, "8.4") || strings.Contains(got, "8.1") {
		t.Fatalf("all must update supported branches only, got %q", got)
	}
}

func TestUpdateRejections(t *testing.T) {
	for body, want := range map[string]int{
		`{"version":"8.1"}`:    http.StatusBadRequest, // EOL
		`{"version":"8.5"}`:    http.StatusNotFound,   // not installed
		`{"version":"8.3;ls"}`: http.StatusBadRequest,
		`not json`:             http.StatusBadRequest,
	} {
		d := newLifecycle(false)
		if rec := post(New(d).Update, body); rec.Code != want || len(d.updated) != 0 {
			t.Errorf("%s: code=%d (want %d) updated=%v", body, rec.Code, want, d.updated)
		}
	}
	d := newLifecycle(false)
	d.fakeDeps.admin = false
	if rec := post(New(d).Update, `{"version":"8.3"}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin code=%d", rec.Code)
	}
	d = newLifecycle(false)
	d.active = &TaskInfo{ID: "busy", Name: "x"}
	if rec := post(New(d).Update, `{"version":"8.3"}`); rec.Code != http.StatusConflict || len(d.updated) != 0 {
		t.Errorf("busy code=%d", rec.Code)
	}
}

func TestUninstallRefusedWhileDomainsAttached(t *testing.T) {
	d := newLifecycle(true)
	rec := post(New(d).Uninstall, `{"version":"8.3"}`)
	if rec.Code != http.StatusConflict || len(d.removed) != 0 {
		t.Fatalf("code=%d removed=%v", rec.Code, d.removed)
	}
	if !strings.Contains(rec.Body.String(), "site.test") {
		t.Fatalf("response must name the attached domain: %s", rec.Body)
	}
}

func TestUninstallRunsAndPassesPurge(t *testing.T) {
	d := newLifecycle(false)
	if rec := post(New(d).Uninstall, `{"version":"8.1","purge":true}`); rec.Code != 200 {
		t.Fatalf("code=%d %s", rec.Code, rec.Body)
	}
	if len(d.removed) != 1 || d.removed[0] != "8.1" || !d.purge {
		t.Fatalf("removed=%v purge=%v", d.removed, d.purge)
	}
	d = newLifecycle(false)
	if rec := post(New(d).Uninstall, `{"version":"9.9"}`); rec.Code != http.StatusNotFound || len(d.removed) != 0 {
		t.Fatalf("not installed code=%d", rec.Code)
	}
	if rec := post(New(d).Uninstall, `{"version":"../8"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid code=%d", rec.Code)
	}
}
