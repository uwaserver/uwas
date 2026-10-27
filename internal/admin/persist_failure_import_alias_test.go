package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/admin/cloudflare"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F27900/F27901/F27902: the unknown-host alias redirect, the Cloudflare zone
// import and the software-library domain attach changed the live domain list
// and saved it through notifyDomainChange, which only logged a failed save. The
// caller got success for a change that vanishes on the next restart.

func persistEnv(t *testing.T, unwritable bool) *hostConflictEnv {
	t.Helper()
	e := newHostConflictEnv(t, []config.Domain{{Host: "example.com", Type: "static"}})
	if unwritable {
		blockConfigPath(t, e.cfgPath)
	}
	return e
}

func TestUnknownAliasRedirectReportsPersistFailure(t *testing.T) {
	const path = "/api/v1/unknown-domains/old.example.org/redirect"
	const body = `{"domain":"example.com"}`
	if code := persistEnv(t, false).do(http.MethodPost, path, body, auth.RoleAdmin); code != http.StatusOK {
		t.Fatalf("control = %d, want 200", code)
	}
	if code := persistEnv(t, true).do(http.MethodPost, path, body, auth.RoleAdmin); code != http.StatusInternalServerError {
		t.Fatalf("EXPECTED: 500 when the configuration cannot be written\nACTUAL: %d", code)
	}
}

func TestCloudflareZoneImportReportsPersistFailure(t *testing.T) {
	origMkdir := cloudflare.MkdirAllFn
	cloudflare.MkdirAllFn = func(string, os.FileMode) error { return nil }
	defer func() { cloudflare.MkdirAllFn = origMkdir }()

	run := func(unwritable bool) int {
		grpDResetCloudflare(t)
		ts := newDNSRecordsServer(t, []cfTestRecord{{Type: "A", Name: "newsite.com", Content: "1.2.3.4"}})
		defer ts.Close()
		cloudflareMu.Lock()
		cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true}
		cloudflareMu.Unlock()
		orig := cfHTTPClient
		cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
		defer func() { cfHTTPClient = orig }()
		e := persistEnv(t, unwritable)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/cloudflare/zones/z/import", strings.NewReader(`{"default_type":"static"}`))
		r = r.WithContext(auth.WithUser(r.Context(), &auth.User{ID: "u", Username: "u", Role: auth.RoleAdmin, Enabled: true}))
		rec := httptest.NewRecorder()
		e.s.mux.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := run(false); code != http.StatusOK {
		t.Fatalf("control = %d, want 200", code)
	}
	if code := run(true); code != http.StatusInternalServerError {
		t.Fatalf("EXPECTED: 500 when the configuration cannot be written\nACTUAL: %d", code)
	}
}

func TestAttachSoftwareDomainReportsPersistFailure(t *testing.T) {
	e := persistEnv(t, false)
	if err := e.s.attachSoftwareDomain("app.example.com", 18080); err != nil {
		t.Fatalf("control: %v", err)
	}
	e = persistEnv(t, true)
	err := e.s.attachSoftwareDomain("app.example.com", 18080)
	if err == nil {
		t.Fatalf("EXPECTED: error when the configuration cannot be written\nACTUAL: nil")
	}
	e.s.configMu.RLock()
	defer e.s.configMu.RUnlock()
	for _, d := range e.s.config.Domains {
		if d.Host == "app.example.com" {
			t.Fatalf("domain left in memory after failed attach")
		}
	}
}
