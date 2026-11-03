package php

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// fakeDeps models a domain user who may manage only `allowed` (or an admin).
type fakeDeps struct {
	mgr             *phpmanager.Manager
	admin           bool
	allowed         map[string]bool
	setFPMAddresses []string
	persistErr      error
	notifyCalls     int
}

func (d *fakeDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !d.admin {
		jsonError(w, "admin access required", http.StatusForbidden)
	}
	return d.admin
}
func (d *fakeDeps) RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool {
	if d.CanManageDomain(r, domain) {
		return true
	}
	jsonError(w, "forbidden", http.StatusForbidden)
	return false
}
func (d *fakeDeps) CanManageDomain(r *http.Request, domain string) bool {
	return d.admin || d.allowed[domain]
}
func (d *fakeDeps) LogInfo(string, ...any)                          {}
func (d *fakeDeps) LogWarn(string, ...any)                          {}
func (d *fakeDeps) LogError(string, ...any)                         {}
func (d *fakeDeps) RecordAudit(*http.Request, string, string, bool) {}
func (d *fakeDeps) ParsePagination(*http.Request) (int, int)        { return 50, 0 }
func (d *fakeDeps) TaskActive() *TaskInfo                           { return nil }
func (d *fakeDeps) TaskSubmit(_, _, _ string, _ func(func(string)) error) *TaskInfo {
	return &TaskInfo{}
}
func (d *fakeDeps) TaskActiveByType(string) *TaskInfo { return nil }
func (d *fakeDeps) TaskLatestByType(string) *TaskInfo { return nil }
func (d *fakeDeps) DomainRoot(string) string          { return "" }
func (d *fakeDeps) SetDomainFPMAddress(_, address string) {
	d.setFPMAddresses = append(d.setFPMAddresses, address)
}
func (d *fakeDeps) PersistConfig() error                 { return d.persistErr }
func (d *fakeDeps) NotifyDomainChange()                  { d.notifyCalls++ }
func (d *fakeDeps) PersistDomainPHPOverrides(string)     {}
func (d *fakeDeps) PHPManager() *phpmanager.Manager      { return d.mgr }
func (d *fakeDeps) PhpRunInstall(string) (string, error) { return "", nil }
func (d *fakeDeps) PhpRunUpdate(string) (string, error)  { return "", nil }
func (d *fakeDeps) PhpRunUninstall(string, bool) (string, error) {
	return "", nil
}

func twoTenantMgr() *phpmanager.Manager {
	m := phpmanager.New(logger.New("error", "text"))
	m.RegisterExistingDomain("tenant-a.com", "8.3", "unix:/run/php/a.sock", "/var/www/a", map[string]string{"memory_limit": "256M"})
	m.RegisterExistingDomain("tenant-b.com", "8.3", "unix:/run/php/b.sock", "/var/www/b", map[string]string{"session.save_path": "tcp://redis:6379?auth=B-SECRET"})
	return m
}

func listDomainNames(t *testing.T, d *fakeDeps) string {
	t.Helper()
	rec := httptest.NewRecorder()
	New(d).DomainsList(rec, httptest.NewRequest("GET", "/api/v1/php/domains", nil))
	var got []phpmanager.DomainPHP
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	var names []string
	for _, g := range got {
		names = append(names, g.Domain)
	}
	return strings.Join(names, ",")
}

// A domain user must not see other tenants' PHP assignments or overrides.
func TestDomainsListScopedToManageableDomains(t *testing.T) {
	if got := listDomainNames(t, &fakeDeps{mgr: twoTenantMgr(), admin: true}); got != "tenant-a.com,tenant-b.com" {
		t.Fatalf("admin list = %q", got)
	}
	if got := listDomainNames(t, &fakeDeps{mgr: twoTenantMgr(), allowed: map[string]bool{"tenant-a.com": true}}); got != "tenant-a.com" {
		t.Fatalf("tenant-a user list = %q, want only tenant-a.com", got)
	}
}

// The server-wide php.ini is admin-only, for reads as well as writes.
func TestConfigRawGetRequiresAdmin(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/php/8.3/config/raw", nil)
	req.SetPathValue("version", "8.3")
	rec := httptest.NewRecorder()
	New(&fakeDeps{mgr: twoTenantMgr(), allowed: map[string]bool{"tenant-a.com": true}}).ConfigRawGet(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin raw php.ini read status = %d, want 403", rec.Code)
	}
}

func TestDomainUnassignClearsAndPersistsRouting(t *testing.T) {
	d := &fakeDeps{mgr: twoTenantMgr(), allowed: map[string]bool{"tenant-a.com": true}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/php/domains/tenant-a.com", nil)
	req.SetPathValue("domain", "tenant-a.com")
	rec := httptest.NewRecorder()

	New(d).DomainUnassign(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unassign status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(d.setFPMAddresses) != 1 || d.setFPMAddresses[0] != "" {
		t.Fatalf("PROBLEM CONFIRMED: routing updates = %q, want one persisted empty FPM address", d.setFPMAddresses)
	}
	if d.notifyCalls != 1 {
		t.Fatalf("domain change notifications = %d, want 1", d.notifyCalls)
	}
	for _, got := range d.mgr.GetDomainInstances() {
		if got.Domain == "tenant-a.com" {
			t.Fatal("unassigned domain remains in PHP manager")
		}
	}
}

func TestDomainUnassignPersistFailureKeepsAssignment(t *testing.T) {
	d := &fakeDeps{mgr: twoTenantMgr(), allowed: map[string]bool{"tenant-a.com": true}, persistErr: errors.New("disk full")}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/php/domains/tenant-a.com", nil)
	req.SetPathValue("domain", "tenant-a.com")
	rec := httptest.NewRecorder()

	New(d).DomainUnassign(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("persist failure status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := d.setFPMAddresses; len(got) != 2 || got[0] != "" || got[1] != "unix:/run/php/a.sock" {
		t.Fatalf("routing updates = %q, want clear followed by rollback", got)
	}
	if d.notifyCalls != 0 {
		t.Fatalf("notifications after persistence failure = %d, want 0", d.notifyCalls)
	}
	found := false
	for _, got := range d.mgr.GetDomainInstances() {
		found = found || got.Domain == "tenant-a.com"
	}
	if !found {
		t.Fatal("assignment was removed even though persistence failed")
	}
}
