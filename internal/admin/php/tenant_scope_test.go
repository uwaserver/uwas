package php

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// fakeDeps models a domain user who may manage only `allowed` (or an admin).
type fakeDeps struct {
	mgr     *phpmanager.Manager
	admin   bool
	allowed map[string]bool
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
func (d *fakeDeps) TaskActiveByType(string) *TaskInfo    { return nil }
func (d *fakeDeps) TaskLatestByType(string) *TaskInfo    { return nil }
func (d *fakeDeps) DomainRoot(string) string             { return "" }
func (d *fakeDeps) SetDomainFPMAddress(string, string)   {}
func (d *fakeDeps) PersistConfig() error                 { return nil }
func (d *fakeDeps) NotifyDomainChange()                  {}
func (d *fakeDeps) PersistDomainPHPOverrides(string)     {}
func (d *fakeDeps) PHPManager() *phpmanager.Manager      { return d.mgr }
func (d *fakeDeps) PhpRunInstall(string) (string, error) { return "", nil }

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
