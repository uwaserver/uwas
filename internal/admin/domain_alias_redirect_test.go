package admin

import (
	"encoding/json"
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

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"},"aliases":["www.example.test"],"alias_redirect_code":302}`)
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

func TestAddDomainAutomaticallyCreatesWWWRedirect(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"}}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want primary + www redirect", len(s.config.Domains))
	}
	redirect := s.config.Domains[1]
	if redirect.Host != "www.example.test" || redirect.Type != "redirect" {
		t.Fatalf("redirect domain = %#v", redirect)
	}
	if redirect.Redirect.Target != "https://example.test" || redirect.Redirect.Status != http.StatusMovedPermanently || !redirect.Redirect.PreservePath {
		t.Fatalf("redirect config = %#v", redirect.Redirect)
	}
}

func TestAddDomainCanUseWWWAsCanonicalHost(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"},"canonical_host":"www"}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want primary www + apex redirect", len(s.config.Domains))
	}
	primary := s.config.Domains[0]
	if primary.Host != "www.example.test" || primary.Type != "static" {
		t.Fatalf("primary domain = %#v", primary)
	}
	redirect := s.config.Domains[1]
	if redirect.Host != "example.test" || redirect.Type != "redirect" {
		t.Fatalf("redirect domain = %#v", redirect)
	}
	if redirect.Redirect.Target != "https://www.example.test" || redirect.Redirect.Status != http.StatusMovedPermanently || !redirect.Redirect.PreservePath {
		t.Fatalf("redirect config = %#v", redirect.Redirect)
	}
}

func TestAddDomainCanServeApexAndWWWWithoutRedirect(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"},"canonical_host":"both"}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 1 {
		t.Fatalf("domains len = %d, want one redirectless domain", len(s.config.Domains))
	}
	primary := s.config.Domains[0]
	if primary.Host != "example.test" || len(primary.Aliases) != 1 || primary.Aliases[0] != "www.example.test" {
		t.Fatalf("primary domain = %#v", primary)
	}
}

func TestAddDomainSkipsAutomaticWWWRedirectWhenAlreadyConfigured(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = []config.Domain{
		{Host: "www.example.test", Type: "redirect", SSL: config.SSLConfig{Mode: "auto"}},
	}

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"}}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 2 {
		t.Fatalf("domains len = %d, want existing www + primary only", len(s.config.Domains))
	}
}

func TestAddRedirectDomainRejectsAliases(t *testing.T) {
	s := testServer()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"go.example.test","type":"redirect","aliases":["www.go.example.test"],"redirect":{"target":"https://example.test","status":301}}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 0 {
		t.Fatalf("domains len = %d, want 0", len(s.config.Domains))
	}
}

func TestUpdateRedirectDomainRejectsAliasesAndClearsLegacyAliases(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{
			Host:    "go.example.test",
			Type:    "redirect",
			Aliases: []string{"legacy.example.test"},
			SSL:     config.SSLConfig{Mode: "auto"},
			Redirect: config.RedirectConfig{
				Target: "https://example.test",
				Status: http.StatusMovedPermanently,
			},
		},
	}

	body := strings.NewReader(`{"aliases":["www.go.example.test"]}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/go.example.test", body)
	req.SetPathValue("host", "go.example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}

	body = strings.NewReader(`{"ssl":{"mode":"auto"}}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/v1/domains/go.example.test", body)
	req.SetPathValue("host", "go.example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("redirect aliases = %#v, want none", s.config.Domains[0].Aliases)
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

	body := strings.NewReader(`{"aliases":["www.example.test"],"alias_redirect_code":302}`)
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

func TestUpdateDomainForceSSLCanBeEnabledAndDisabled(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "example.test", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
	}

	body := strings.NewReader(`{"ssl":{"mode":"auto","force_ssl":true}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !s.config.Domains[0].SSL.ForceSSL {
		t.Fatal("force_ssl should be enabled")
	}

	body = strings.NewReader(`{"ssl":{"mode":"auto","force_ssl":false}}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if s.config.Domains[0].SSL.ForceSSL {
		t.Fatal("force_ssl should be disabled by explicit false")
	}
}

func TestHandleDomainsIncludesForceSSL(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "example.test", Type: "static", SSL: config.SSLConfig{Mode: "auto", ForceSSL: true}},
	}

	rec := httptest.NewRecorder()
	s.handleDomains(rec, httptest.NewRequest("GET", "/api/v1/domains", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []struct {
			Host     string `json:"host"`
			ForceSSL bool   `json:"force_ssl"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Host != "example.test" || !out.Items[0].ForceSSL {
		t.Fatalf("items = %#v, want example.test with force_ssl=true", out.Items)
	}
}
