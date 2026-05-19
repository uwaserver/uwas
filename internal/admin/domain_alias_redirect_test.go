package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestAddDomainDropsSameSiteWWWAlias(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"},"aliases":["www.example.test"],"alias_redirect_code":302}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}

	if len(s.config.Domains) != 1 {
		t.Fatalf("domains len = %d, want 1", len(s.config.Domains))
	}
	if len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("primary aliases = %#v, want none for implicit www", s.config.Domains[0].Aliases)
	}
}

func TestAddDomainImplicitlyServesWWWWithoutSeparateRecord(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"}}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 1 {
		t.Fatalf("domains len = %d, want one primary record", len(s.config.Domains))
	}
	if s.config.Domains[0].Host != "example.test" || len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("primary domain = %#v, want apex without explicit www alias", s.config.Domains[0])
	}
	if got := domainHostnames(s.config.Domains[0]); len(got) != 2 || got[0] != "example.test" || got[1] != "www.example.test" {
		t.Fatalf("domainHostnames = %#v, want apex + implicit www", got)
	}
}

func TestAddDomainCanonicalizesWWWHostToApex(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = nil

	body := strings.NewReader(`{"host":"www.example.test","type":"static","ssl":{"mode":"auto"},"canonical_host":"www"}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 1 {
		t.Fatalf("domains len = %d, want one apex record", len(s.config.Domains))
	}
	primary := s.config.Domains[0]
	if primary.Host != "example.test" || primary.Type != "static" || primary.CanonicalHost != "www" || len(primary.Aliases) != 0 {
		t.Fatalf("primary domain = %#v", primary)
	}
	if got := domainHostnames(primary); len(got) != 2 || got[0] != "www.example.test" || got[1] != "example.test" {
		t.Fatalf("domainHostnames = %#v, want www first because canonical_host=www", got)
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
	if primary.Host != "example.test" || primary.CanonicalHost != "apex" || len(primary.Aliases) != 0 {
		t.Fatalf("primary domain = %#v", primary)
	}
}

func TestAddDomainRemovesLegacyImplicitWWWRedirectWhenAlreadyConfigured(t *testing.T) {
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Domains = []config.Domain{
		newCanonicalRedirectAliasDomain("www.example.test", "example.test", http.StatusMovedPermanently, true),
	}

	body := strings.NewReader(`{"host":"example.test","type":"static","ssl":{"mode":"auto"}}`)
	rec := httptest.NewRecorder()
	s.handleAddDomain(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	if len(s.config.Domains) != 1 {
		t.Fatalf("domains len = %d, want legacy www redirect removed", len(s.config.Domains))
	}
	if s.config.Domains[0].Host != "example.test" {
		t.Fatalf("domain = %#v, want example.test", s.config.Domains[0])
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

func TestUpdateDomainDropsSameSiteWWWAlias(t *testing.T) {
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

	if len(s.config.Domains) != 1 {
		t.Fatalf("domains len = %d, want 1", len(s.config.Domains))
	}
	if len(s.config.Domains[0].Aliases) != 0 {
		t.Fatalf("primary aliases = %#v, want none after same-site www cleanup", s.config.Domains[0].Aliases)
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

func TestUpdateDomainCanonicalHostPersistsInConfig(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "example.test", Type: "proxy", SSL: config.SSLConfig{Mode: "auto"}, Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:3000"}}}},
	}

	body := strings.NewReader(`{"canonical_host":"www"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if s.config.Domains[0].Host != "example.test" {
		t.Fatalf("host = %q, want apex config host", s.config.Domains[0].Host)
	}
	if s.config.Domains[0].CanonicalHost != "www" {
		t.Fatalf("canonical_host = %q, want www", s.config.Domains[0].CanonicalHost)
	}
	if got := domainHostnames(s.config.Domains[0]); len(got) != 2 || got[0] != "www.example.test" || got[1] != "example.test" {
		t.Fatalf("domainHostnames = %#v, want www first", got)
	}
}

func TestUpdateDomainForceSSLPersistsToDomainFile(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.SetConfigPath(filepath.Join(dir, "uwas.yaml"))
	s.config.Domains = []config.Domain{
		{Host: "example.test", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
	}

	body := strings.NewReader(`{"ssl":{"mode":"auto","force_ssl":true}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/domains/example.test", body)
	req.SetPathValue("host", "example.test")
	s.handleUpdateDomain(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "domains.d", "example.test.yaml"))
	if err != nil {
		t.Fatalf("read persisted domain: %v", err)
	}
	if !strings.Contains(string(data), "force_ssl: true") {
		t.Fatalf("persisted domain missing force_ssl: true:\n%s", string(data))
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
