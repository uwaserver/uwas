package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F59600: the update handler dropped the implicit www redirect that sits
// before the edited domain, which shifts the edited domain's index, then
// passed the stale index to the alias upsert as its skip index. The upsert
// skipped the wrong record, missed the existing redirect alias and appended a
// second record for the same host, which the next start rejects as a
// duplicate host.
func TestUpdateDomainRedirectAliasUpsertAfterImplicitWWWRemoval(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	redirect := func(host string) config.Domain {
		return config.Domain{
			Host: host, Type: "redirect", SSL: config.SSLConfig{Mode: "auto"},
			Redirect: config.RedirectConfig{Target: "https://example.test", Status: 301},
		}
	}
	s.config.Domains = []config.Domain{
		redirect("www.example.test"),
		{Host: "example.test", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		redirect("alias.example.test"),
	}

	body := strings.NewReader(`{"aliases":["alias.example.test"],"alias_mode":"redirect","alias_redirect_code":301}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	seen := map[string]int{}
	for _, d := range s.config.Domains {
		seen[d.Host]++
	}
	for host, n := range seen {
		if n != 1 {
			t.Fatalf("host %q appears %d times in %#v, want once", host, n, s.config.Domains)
		}
	}
	if seen["example.test"] != 1 || seen["alias.example.test"] != 1 {
		t.Fatalf("domains = %#v, want example.test and alias.example.test once each", s.config.Domains)
	}
}

// Control: no implicit www record before the edited domain, same request.
func TestUpdateDomainRedirectAliasUpsertWithoutImplicitWWW(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = []config.Domain{
		{Host: "example.test", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		{Host: "alias.example.test", Type: "redirect", SSL: config.SSLConfig{Mode: "auto"},
			Redirect: config.RedirectConfig{Target: "https://example.test", Status: 301}},
	}
	body := strings.NewReader(`{"aliases":["alias.example.test"],"alias_mode":"redirect","alias_redirect_code":301}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 2 {
		t.Fatalf("domains = %#v, want 2", s.config.Domains)
	}
}
