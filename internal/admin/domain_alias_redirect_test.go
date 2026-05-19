package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestAddDomainRedirectAliasesCreateCanonicalRedirectDomains(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"},"aliases":["www.example.test"],"alias_mode":"redirect","alias_redirect_code":302}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}

	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want 2", len(s.config.Domains))
	}
	if len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("primary aliases = %#v, want none for redirect aliases", s.config.Domains[0].Aliases)
	}
	redirect := s.config.Domains[1]
	if redirect.Host != "www.example.test" || redirect.Type != "redirect" {
		t.Fatalf("redirect domain = %#v", redirect)
	}
	if redirect.SSL.Mode != "auto" {
		t.Fatalf("redirect SSL mode = %q, want auto", redirect.SSL.Mode)
	}
	if redirect.Redirect.Target != "https://example.test" || redirect.Redirect.Status != 302 || !redirect.Redirect.PreservePath {
		t.Fatalf("redirect config = %#v", redirect.Redirect)
	}
}

func TestAddDomainRejectsSelfAlias(t *testing.T) {
	s := testServer()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","aliases":["example.test"]}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestAddDomainRejectsAliasOwnedByAnotherDomain(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "other.test", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	body := strings.NewReader(`{"host":"example.test","type":"static","aliases":["other.test"],"alias_mode":"redirect","alias_redirect_code":301}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateDomainConvertsAliasesToCanonicalRedirectDomains(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "example.test", Aliases: []string{"www.example.test"}, Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
	}

	body := strings.NewReader(`{"aliases":["www.example.test"],"alias_mode":"redirect","alias_redirect_code":302}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want 2", len(s.config.Domains))
	}
	if len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("primary aliases = %#v, want none after conversion", s.config.Domains[0].Aliases)
	}
	redirect := s.config.Domains[1]
	if redirect.Host != "www.example.test" || redirect.Type != "redirect" {
		t.Fatalf("redirect domain = %#v", redirect)
	}
	if redirect.Redirect.Target != "https://example.test" || redirect.Redirect.Status != 302 {
		t.Fatalf("redirect config = %#v", redirect.Redirect)
	}
}
