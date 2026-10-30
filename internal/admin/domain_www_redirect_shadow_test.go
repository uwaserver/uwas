package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func wwwShadowDomains() []config.Domain {
	return []config.Domain{
		{
			Host: "www.example.test", Type: "redirect", SSL: config.SSLConfig{Mode: "auto"},
			Redirect: config.RedirectConfig{Target: "https://example.test", Status: 301},
		},
		{Host: "example.test", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
	}
}

// F59601: records are matched with the "www." prefix ignored, so a legacy or
// migrated redirect record "www.example.test" listed before the site answered
// to the site's name. Update patched the redirect, Delete removed it and kept
// the site.
func TestUpdateDomainTargetsSiteNotWWWRedirectRecord(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = wwwShadowDomains()

	body := strings.NewReader(`{"ssl":{"mode":"auto","force_ssl":true}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	assertSitePatched(t, s.config.Domains)
}

func TestDeleteDomainRemovesSiteNotWWWRedirectRecord(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = wwwShadowDomains()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/domains/example.test?confirm=true", nil)
	req.SetPathValue("host", "example.test")
	s.handleDeleteDomain(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	for _, d := range s.config.Domains {
		if d.Host == "example.test" {
			t.Fatalf("the site survived its own delete: %#v", s.config.Domains)
		}
	}
}

// Control: with the site first, the same requests already hit the site.
func TestUpdateDomainControlSiteFirst(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	d := wwwShadowDomains()
	s.config.Domains = []config.Domain{d[1], d[0]}
	body := strings.NewReader(`{"ssl":{"mode":"auto","force_ssl":true}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	assertSitePatched(t, s.config.Domains)
}

// assertSitePatched checks the record spelled "example.test" is still the
// static site and carries the patch. The update drops the implicit www
// redirect record, so the slice positions are not compared.
func assertSitePatched(t *testing.T, domains []config.Domain) {
	t.Helper()
	for _, d := range domains {
		if d.Host != "example.test" {
			continue
		}
		if d.Type != "static" || !d.SSL.ForceSSL {
			t.Fatalf("site record = type %q force_ssl %v, want static and patched; domains: %#v", d.Type, d.SSL.ForceSSL, domains)
		}
		return
	}
	t.Fatalf("no example.test record in %#v", domains)
}
