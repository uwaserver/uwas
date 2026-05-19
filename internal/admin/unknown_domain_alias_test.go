package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

func TestUnknownDomainAliasCanCreateCanonicalRedirect(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{
			Host:    "dgnteknoloji.com",
			Type:    "static",
			Aliases: []string{"www.dgnteknoloji.com"},
			SSL:     config.SSLConfig{Mode: "auto"},
		},
	}
	tracker := router.NewUnknownHostTracker()
	tracker.Record("www.dgnteknoloji.com")
	s.SetUnknownHostTracker(tracker)

	req := httptest.NewRequest("POST", "/api/v1/unknown-domains/www.dgnteknoloji.com/alias", strings.NewReader(`{"domain":"dgnteknoloji.com","mode":"redirect","redirect_code":302}`))
	req.SetPathValue("host", "www.dgnteknoloji.com")
	rec := httptest.NewRecorder()

	s.handleUnknownDomainsAlias(rec, withAdminContext(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want 2", len(s.config.Domains))
	}
	if len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("redirect alias should be removed from same-site aliases: %#v", s.config.Domains[0].Aliases)
	}
	redirectDomain := s.config.Domains[1]
	if redirectDomain.Host != "www.dgnteknoloji.com" || redirectDomain.Type != "redirect" {
		t.Fatalf("unexpected redirect domain: %#v", redirectDomain)
	}
	if redirectDomain.SSL.Mode != "auto" {
		t.Fatalf("redirect alias ssl mode = %q, want auto", redirectDomain.SSL.Mode)
	}
	if redirectDomain.Redirect.Target != "https://dgnteknoloji.com" ||
		redirectDomain.Redirect.Status != http.StatusFound ||
		!redirectDomain.Redirect.PreservePath {
		t.Fatalf("unexpected redirect config: %#v", redirectDomain.Redirect)
	}
	if tracker.IsBlocked("www.dgnteknoloji.com") {
		t.Fatal("redirected unknown host should be unblocked")
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "redirect" || body["domain"] != "dgnteknoloji.com" {
		t.Fatalf("unexpected response body: %#v", body)
	}
}

func TestUnknownDomainRedirectEndpointUpdatesExistingRedirect(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "example.com", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		{
			Host: "www.example.com",
			Type: "redirect",
			SSL:  config.SSLConfig{Mode: "auto"},
			Redirect: config.RedirectConfig{
				Target:       "https://old.example.com",
				Status:       http.StatusMovedPermanently,
				PreservePath: true,
			},
		},
	}

	req := httptest.NewRequest("POST", "/api/v1/unknown-domains/www.example.com/redirect", strings.NewReader(`{"domain":"example.com","redirect_code":302,"preserve_path":false}`))
	req.SetPathValue("host", "www.example.com")
	rec := httptest.NewRecorder()

	s.handleUnknownDomainsAlias(rec, withAdminContext(req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want 2", len(s.config.Domains))
	}
	redirectDomain := s.config.Domains[1]
	if redirectDomain.Redirect.Target != "https://example.com" ||
		redirectDomain.Redirect.Status != http.StatusFound ||
		redirectDomain.Redirect.PreservePath {
		t.Fatalf("unexpected redirect config: %#v", redirectDomain.Redirect)
	}
}
