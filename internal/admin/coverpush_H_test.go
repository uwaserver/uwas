package admin

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// grpHValidTOTPCode computes a currently-valid TOTP code for the given secret,
// mirroring ValidateTOTP/generateCode in totp.go.
func grpHValidTOTPCode(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	// Match ValidateTOTP's current step. ValidateTOTP allows ±1 step so a slight
	// boundary skew is tolerated.
	return generateCode(key, uint64(time.Now().Unix()/30))
}

// =============================================================================
// handleUpdateDomain
// =============================================================================

func grpHUpdateReq(method, host, query string, body any, reseller bool, t *testing.T) *http.Request {
	t.Helper()
	var buf []byte
	switch v := body.(type) {
	case string:
		buf = []byte(v)
	case nil:
		buf = nil
	default:
		var err error
		buf, err = json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	target := "/api/v1/domains/" + host
	if query != "" {
		target += "?" + query
	}
	var r *http.Request
	if buf == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(buf))
	}
	r.SetPathValue("host", host)
	if reseller {
		r = withResellerContext(r)
	} else {
		r = withAdminContext(r)
	}
	return r
}

func TestGrpH_UpdateDomainFull(t *testing.T) {
	t.Run("merge mode updates field and persists", func(t *testing.T) {
		s := testServer()
		r := grpHUpdateReq("PUT", "example.com", "", map[string]any{"ip": "9.9.9.9"}, false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		found := false
		for _, d := range s.config.Domains {
			if canonicalDomainHostname(d.Host) == "example.com" {
				found = true
				if d.IP != "9.9.9.9" {
					t.Errorf("IP not persisted: %q", d.IP)
				}
			}
		}
		if !found {
			t.Fatal("example.com missing after update")
		}
	})

	t.Run("replace mode resets unspecified fields", func(t *testing.T) {
		s := testServer()
		// Seed an alias on example.com.
		for i := range s.config.Domains {
			if s.config.Domains[i].Host == "example.com" {
				s.config.Domains[i].Aliases = []string{"old.example.com"}
			}
		}
		r := grpHUpdateReq("PUT", "example.com", "replace=true",
			map[string]any{"type": "static", "aliases": []string{}}, false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		for _, d := range s.config.Domains {
			if d.Host == "example.com" && len(d.Aliases) != 0 {
				t.Errorf("replace mode kept aliases: %v", d.Aliases)
			}
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		s := testServer()
		r := grpHUpdateReq("PUT", "example.com", "", "{not json", false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})

	t.Run("invalid hostname rename", func(t *testing.T) {
		s := testServer()
		r := grpHUpdateReq("PUT", "example.com", "", map[string]any{"host": "bad host!!"}, false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := testServer()
		r := grpHUpdateReq("PUT", "missing.com", "", map[string]any{"ip": "1.1.1.1"}, false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want 404", rec.Code)
		}
	})

	t.Run("reseller forbidden domain", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		// reseller manages reseller.com only; example.com forbidden.
		r := grpHUpdateReq("PUT", "example.com", "", map[string]any{"ip": "1.1.1.1"}, true, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want 403", rec.Code)
		}
	})

	t.Run("reseller mass-assignment rejected", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		// Add reseller.com so domain access passes, then send sensitive fields.
		s.config.Domains = append(s.config.Domains, config.Domain{Host: "reseller.com", Type: "static"})
		r := grpHUpdateReq("PUT", "reseller.com", "",
			map[string]any{"ssl": map[string]any{"mode": "auto"}, "security": map[string]any{}}, true, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "cannot modify") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("reseller cannot over-post structural fields", func(t *testing.T) {
		// Regression for VULN-001/VULN-003: root/type/proxy/redirect/ip must be
		// admin-only on update, else a reseller escalates to filesystem escape,
		// route hijack, or SSRF on a domain they otherwise legitimately own.
		for field, val := range map[string]any{
			"root": "/",
			"type": "proxy",
			"ip":   "10.0.0.1",
		} {
			s := testServer()
			s.authMgr = newMockAuthManager()
			s.config.Domains = append(s.config.Domains, config.Domain{Host: "reseller.com", Type: "static"})
			r := grpHUpdateReq("PUT", "reseller.com", "", map[string]any{field: val}, true, t)
			rec := httptest.NewRecorder()
			s.handleUpdateDomain(rec, r)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("field %q: status=%d body=%s want 403", field, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("admin root must stay under web root", func(t *testing.T) {
		// Regression for VULN-001: the update path enforces the same
		// root-under-webroot containment as the create path, for all users.
		s := testServer()
		s.config.Global.WebRoot = "/var/www"
		s.config.Domains = append(s.config.Domains, config.Domain{Host: "admin-owned.com", Type: "static"})
		r := grpHUpdateReq("PUT", "admin-owned.com", "", map[string]any{"root": "/etc"}, false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "must be under") {
			t.Errorf("unexpected body: %s", rec.Body.String())
		}
	})

	t.Run("reseller rename to unauthorized domain", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		s.config.Domains = append(s.config.Domains, config.Domain{Host: "reseller.com", Type: "static"})
		r := grpHUpdateReq("PUT", "reseller.com", "", map[string]any{"host": "other.com"}, true, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
		}
	})

	t.Run("redirect-with-aliases rejected", func(t *testing.T) {
		s := testServer()
		s.config.Domains = append(s.config.Domains, config.Domain{
			Host: "redir.example.com", Type: "redirect",
			Redirect: config.RedirectConfig{Target: "https://example.com"},
		})
		r := grpHUpdateReq("PUT", "redir.example.com", "",
			map[string]any{"aliases": []string{"alias.example.com"}}, false, t)
		rec := httptest.NewRecorder()
		s.handleUpdateDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
		}
	})
}

// =============================================================================
// handleAddDomain
// =============================================================================

func TestGrpH_AddDomain(t *testing.T) {
	t.Run("add success", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/domains",
			bytes.NewReader([]byte(`{"host":"newsite.com","type":"proxy","proxy":{"upstreams":[{"address":"http://127.0.0.1:8080"}]}}`)))
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s want 201", rec.Code, rec.Body.String())
		}
		ok := false
		for _, d := range s.config.Domains {
			if d.Host == "newsite.com" {
				ok = true
			}
		}
		if !ok {
			t.Fatal("newsite.com not added to config")
		}
	})

	t.Run("missing host", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains",
			bytes.NewReader([]byte(`{"type":"static"}`))))
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains",
			bytes.NewReader([]byte(`{bad`))))
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("invalid hostname", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains",
			bytes.NewReader([]byte(`{"host":"bad host!!","type":"static"}`))))
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("duplicate hostname conflict", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains",
			bytes.NewReader([]byte(`{"host":"example.com","type":"static"}`))))
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s want 409", rec.Code, rec.Body.String())
		}
	})

	t.Run("redirect with aliases rejected", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains",
			bytes.NewReader([]byte(`{"host":"r.com","type":"redirect","aliases":["a.r.com"],"redirect":{"to":"https://x.com"}}`))))
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("reseller forbidden via query host", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		r := withResellerContext(httptest.NewRequest("POST", "/api/v1/domains?host=forbidden.com",
			bytes.NewReader([]byte(`{"host":"forbidden.com","type":"static"}`))))
		rec := httptest.NewRecorder()
		s.handleAddDomain(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})
}

// =============================================================================
// handleDeleteDomain + removeDomainFile
// =============================================================================

func TestGrpH_DeleteDomain(t *testing.T) {
	t.Run("missing confirm", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("DELETE", "/api/v1/domains/example.com", nil)
		r.SetPathValue("host", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleDeleteDomain(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("DELETE", "/api/v1/domains/missing.com?confirm=true", nil)
		r.SetPathValue("host", "missing.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleDeleteDomain(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d want 404", rec.Code)
		}
	})

	t.Run("protected default domain", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("DELETE", "/api/v1/domains/localhost?confirm=true", nil)
		r.SetPathValue("host", "localhost")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleDeleteDomain(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("reseller forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		r := httptest.NewRequest("DELETE", "/api/v1/domains/example.com?confirm=true", nil)
		r.SetPathValue("host", "example.com")
		r = withResellerContext(r)
		rec := httptest.NewRecorder()
		s.handleDeleteDomain(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("success removes domain file", func(t *testing.T) {
		s := testServer()
		dir := t.TempDir()
		s.configPath = filepath.Join(dir, "uwas.yaml")
		domainsDir := filepath.Join(dir, "domains.d")
		if err := os.MkdirAll(domainsDir, 0755); err != nil {
			t.Fatal(err)
		}
		yamlPath := filepath.Join(domainsDir, "example.com.yaml")
		if err := os.WriteFile(yamlPath, []byte("host: example.com\n"), 0644); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("DELETE", "/api/v1/domains/example.com?confirm=true", nil)
		r.SetPathValue("host", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleDeleteDomain(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
			t.Errorf("domain yaml not removed: err=%v", err)
		}
		for _, d := range s.config.Domains {
			if canonicalDomainHostname(d.Host) == "example.com" {
				t.Error("example.com still in config after delete")
			}
		}
	})
}

// =============================================================================
// handleDomainDetail + handleDomains (list)
// =============================================================================

func TestGrpH_DomainDetail(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("GET", "/api/v1/domains/example.com", nil)
		r.SetPathValue("host", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleDomainDetail(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out config.Domain
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Host != "example.com" {
			t.Errorf("host=%q", out.Host)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("GET", "/api/v1/domains/missing.com", nil)
		r.SetPathValue("host", "missing.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleDomainDetail(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d want 404", rec.Code)
		}
	})

	t.Run("reseller forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		r := httptest.NewRequest("GET", "/api/v1/domains/example.com", nil)
		r.SetPathValue("host", "example.com")
		r = withResellerContext(r)
		rec := httptest.NewRecorder()
		s.handleDomainDetail(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})
}

func TestGrpH_DomainsList(t *testing.T) {
	t.Run("admin sees all", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("GET", "/api/v1/domains", nil))
		rec := httptest.NewRecorder()
		s.handleDomains(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d", rec.Code)
		}
		var resp struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Items) < 2 {
			t.Errorf("expected >=2 domains, got %d", len(resp.Items))
		}
	})

	t.Run("reseller filtered", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		s.config.Domains = append(s.config.Domains, config.Domain{Host: "reseller.com", Type: "static"})
		r := withResellerContext(httptest.NewRequest("GET", "/api/v1/domains", nil))
		rec := httptest.NewRecorder()
		s.handleDomains(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d", rec.Code)
		}
		var resp struct {
			Items []map[string]any `json:"items"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		for _, it := range resp.Items {
			if it["host"] != "reseller.com" {
				t.Errorf("reseller saw unauthorized domain: %v", it["host"])
			}
		}
	})
}

// =============================================================================
// validateDomainConfig / validateDomainUpdateConfig + hostname helpers
// =============================================================================

func TestGrpH_ValidateDomainConfig(t *testing.T) {
	t.Run("valid static under webroot", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "site", "public_html")
		os.MkdirAll(root, 0755)
		s := testServer()
		s.config.Global.WebRoot = dir
		d := &config.Domain{Host: "ok.com", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}}
		if err := validateDomainConfig(d, s); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("root outside webroot", func(t *testing.T) {
		dir := t.TempDir()
		other := t.TempDir()
		outside := filepath.Join(other, "evil")
		os.MkdirAll(outside, 0755)
		s := testServer()
		s.config.Global.WebRoot = dir
		d := &config.Domain{Host: "bad.com", Type: "static", Root: outside, SSL: config.SSLConfig{Mode: "off"}}
		if err := validateDomainConfig(d, s); err == nil {
			t.Fatal("expected error for root outside webroot")
		}
	})

	t.Run("php type with no active versions (phpMgr set)", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		d := &config.Domain{Host: "php.com", Type: "php", SSL: config.SSLConfig{Mode: "off"}}
		err := validateDomainConfig(d, s)
		if err == nil || !strings.Contains(err.Error(), "PHP") {
			t.Fatalf("expected PHP availability error, got %v", err)
		}
	})

	t.Run("php type with nil phpMgr is ok for shape", func(t *testing.T) {
		s := testServer()
		// phpMgr nil — the PHP-availability check is skipped. A php domain still
		// needs a valid base config; provide an fpm address to satisfy it.
		d := &config.Domain{Host: "php2.com", Type: "php", SSL: config.SSLConfig{Mode: "off"},
			PHP: config.PHPConfig{FPMAddress: "127.0.0.1:9000"}}
		if err := validateDomainConfig(d, s); err != nil {
			// Some base validators may still require more; only assert it's not
			// the runtime PHP-availability error.
			if strings.Contains(err.Error(), "no active PHP") {
				t.Fatalf("unexpected PHP availability error with nil phpMgr: %v", err)
			}
		}
	})

	t.Run("update config partial", func(t *testing.T) {
		d := &config.Domain{Host: "x.com", Type: "static"}
		if err := validateDomainUpdateConfig(d, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestGrpH_HostnameHelpers(t *testing.T) {
	t.Run("implicitWWWHostname", func(t *testing.T) {
		cases := map[string]string{
			"example.com":   "www.example.com",
			"www.x.com":     "www.x.com",
			"localhost":     "",
			"*.example.com": "",
			"":              "",
		}
		for in, want := range cases {
			if got := implicitWWWHostname(in); got != want {
				t.Errorf("implicitWWWHostname(%q)=%q want %q", in, got, want)
			}
		}
	})

	t.Run("domainHostnames apex preference", func(t *testing.T) {
		d := config.Domain{Host: "example.com", Type: "static"}
		got := domainHostnames(d)
		if len(got) != 2 || got[0] != "example.com" || got[1] != "www.example.com" {
			t.Errorf("domainHostnames apex = %v", got)
		}
	})

	t.Run("domainHostnames www preference", func(t *testing.T) {
		d := config.Domain{Host: "example.com", Type: "static", CanonicalHost: "www"}
		got := domainHostnames(d)
		if len(got) == 0 || got[0] != "www.example.com" {
			t.Errorf("domainHostnames www-pref = %v", got)
		}
	})

	t.Run("normalizeDomainHostnames dedups aliases", func(t *testing.T) {
		d := &config.Domain{Host: "WWW.Example.com", Type: "static",
			Aliases: []string{"example.com", "Foo.com", "foo.com", ""}}
		normalizeDomainHostnames(d)
		if d.Host != "example.com" {
			t.Errorf("host = %q", d.Host)
		}
		// "example.com" alias equals host → dropped; "foo.com" dedup'd to one.
		if len(d.Aliases) != 1 || d.Aliases[0] != "foo.com" {
			t.Errorf("aliases = %v", d.Aliases)
		}
	})

	t.Run("normalizeDomainHostnames redirect clears canonical", func(t *testing.T) {
		d := &config.Domain{Host: "r.com", Type: "redirect", CanonicalHost: "www"}
		normalizeDomainHostnames(d)
		if d.CanonicalHost != "" {
			t.Errorf("redirect canonical not cleared: %q", d.CanonicalHost)
		}
	})

	t.Run("findDomainHostnameConflictAllowingRedirect", func(t *testing.T) {
		domains := []config.Domain{
			{Host: "example.com", Type: "static"},
			{Host: "other.com", Type: "static", Aliases: []string{"alias.com"}},
		}
		if got := findDomainHostnameConflictAllowingRedirect(domains, 0, "other.com", "example.com"); got != "other.com" {
			t.Errorf("conflict = %q want other.com", got)
		}
		if got := findDomainHostnameConflictAllowingRedirect(domains, 0, "alias.com", "example.com"); got != "other.com" {
			t.Errorf("alias conflict = %q want other.com", got)
		}
		if got := findDomainHostnameConflictAllowingRedirect(domains, 0, "free.com", "example.com"); got != "" {
			t.Errorf("expected no conflict, got %q", got)
		}
		if got := findDomainHostnameConflictAllowingRedirect(domains, -1, "", "example.com"); got != "" {
			t.Errorf("empty host conflict = %q", got)
		}
	})
}

// =============================================================================
// handleCerts + handleCertRenew
// =============================================================================

func TestGrpH_Certs(t *testing.T) {
	t.Run("list with nil tlsMgr", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("GET", "/api/v1/certs", nil))
		rec := httptest.NewRecorder()
		s.handleCerts(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var certs []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &certs); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// example.com (auto) → pending status expected.
		foundPending := false
		for _, c := range certs {
			if c["status"] == "pending" {
				foundPending = true
			}
		}
		if !foundPending {
			t.Errorf("expected a pending cert, got %v", certs)
		}
	})

	t.Run("list reseller forbidden", func(t *testing.T) {
		s := testServer()
		r := withResellerContext(httptest.NewRequest("GET", "/api/v1/certs", nil))
		rec := httptest.NewRecorder()
		s.handleCerts(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("renew with nil tlsMgr", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/certs/example.com/renew", nil)
		r.SetPathValue("host", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleCertRenew(rec, r)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d want 503", rec.Code)
		}
	})

	t.Run("renew reseller forbidden", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/certs/example.com/renew", nil)
		r.SetPathValue("host", "example.com")
		r = withResellerContext(r)
		rec := httptest.NewRecorder()
		s.handleCertRenew(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})
}

// =============================================================================
// 2FA: setup / verify / disable
// =============================================================================

func TestGrpH_2FA(t *testing.T) {
	t.Run("setup generates pending secret", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/setup", nil))
		rec := httptest.NewRecorder()
		s.handle2FASetup(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		if out["secret"] == "" || out["uri"] == "" {
			t.Errorf("missing secret/uri: %v", out)
		}
	})

	t.Run("setup conflict when already enabled", func(t *testing.T) {
		s := testServer()
		s.config.Global.Admin.TOTPSecret = "EXISTINGSECRET"
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/setup", nil))
		rec := httptest.NewRecorder()
		s.handle2FASetup(rec, r)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d want 409", rec.Code)
		}
	})

	t.Run("verify invalid JSON", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/verify",
			bytes.NewReader([]byte("{bad"))))
		rec := httptest.NewRecorder()
		s.handle2FAVerify(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("verify no pending setup", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/verify",
			bytes.NewReader([]byte(`{"code":"123456"}`))))
		rec := httptest.NewRecorder()
		s.handle2FAVerify(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
		}
	})

	t.Run("verify invalid code", func(t *testing.T) {
		s := testServer()
		secret, _ := GenerateTOTPSecret()
		s.pendingTOTP = map[string]string{"admin": secret}
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/verify",
			bytes.NewReader([]byte(`{"code":"000000"}`))))
		rec := httptest.NewRecorder()
		s.handle2FAVerify(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d want 401", rec.Code)
		}
	})

	t.Run("verify success activates pending", func(t *testing.T) {
		s := testServer()
		secret, _ := GenerateTOTPSecret()
		s.pendingTOTP = map[string]string{"admin": secret}
		code := grpHValidTOTPCode(t, secret)
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/verify",
			bytes.NewReader([]byte(`{"code":"`+code+`"}`))))
		rec := httptest.NewRecorder()
		s.handle2FAVerify(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		if s.config.Global.Admin.TOTPSecret != secret {
			t.Error("pending secret not activated")
		}
	})

	t.Run("disable not enabled", func(t *testing.T) {
		s := testServer()
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/disable",
			bytes.NewReader([]byte(`{"code":"123456"}`))))
		rec := httptest.NewRecorder()
		s.handle2FADisable(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("disable invalid code", func(t *testing.T) {
		s := testServer()
		secret, _ := GenerateTOTPSecret()
		s.config.Global.Admin.TOTPSecret = secret
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/disable",
			bytes.NewReader([]byte(`{"code":"000000"}`))))
		rec := httptest.NewRecorder()
		s.handle2FADisable(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d want 401", rec.Code)
		}
	})

	t.Run("disable success", func(t *testing.T) {
		s := testServer()
		secret, _ := GenerateTOTPSecret()
		s.config.Global.Admin.TOTPSecret = secret
		code := grpHValidTOTPCode(t, secret)
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/2fa/disable",
			bytes.NewReader([]byte(`{"code":"`+code+`"}`))))
		rec := httptest.NewRecorder()
		s.handle2FADisable(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		if s.config.Global.Admin.TOTPSecret != "" {
			t.Error("secret not cleared after disable")
		}
	})
}

// =============================================================================
// handleAuthBootstrap
// =============================================================================

func TestGrpH_AuthBootstrap(t *testing.T) {
	t.Run("auth not enabled", func(t *testing.T) {
		s := testServer()
		// authMgr nil and Users.Enabled false → ensureAuthManagerFromConfig keeps nil.
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
			bytes.NewReader([]byte(`{"username":"a","password":"password"}`))))
		rec := httptest.NewRecorder()
		s.handleAuthBootstrap(rec, r)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d want 501", rec.Code)
		}
	})

	t.Run("not available when apiKey set", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		s.config.Global.Users.Enabled = true
		s.config.Global.Admin.APIKey = "somekey"
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
			bytes.NewReader([]byte(`{"username":"a","password":"password"}`))))
		rec := httptest.NewRecorder()
		s.handleAuthBootstrap(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("already complete (users exist)", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager() // starts with 2 users
		s.config.Global.Users.Enabled = true
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
			bytes.NewReader([]byte(`{"username":"a","password":"password"}`))))
		rec := httptest.NewRecorder()
		s.handleAuthBootstrap(rec, r)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d want 409", rec.Code)
		}
	})

	t.Run("missing input", func(t *testing.T) {
		s := testServer()
		empty := newMockAuthManager()
		empty.users = map[string]*auth.User{} // empty user list
		s.authMgr = empty
		s.config.Global.Users.Enabled = true
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
			bytes.NewReader([]byte(`{"username":"","password":""}`))))
		rec := httptest.NewRecorder()
		s.handleAuthBootstrap(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
		}
	})

	t.Run("success creates first admin", func(t *testing.T) {
		s := testServer()
		empty := newMockAuthManager()
		empty.users = map[string]*auth.User{}
		s.authMgr = empty
		s.config.Global.Users.Enabled = true
		r := withAdminContext(httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
			bytes.NewReader([]byte(`{"username":"root","email":"r@x.com","password":"S3cure-Passw0rd!"}`))))
		rec := httptest.NewRecorder()
		s.handleAuthBootstrap(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		if out["status"] != "authenticated" {
			t.Errorf("unexpected status: %v", out)
		}
	})
}

// =============================================================================
// PHP config handlers
// =============================================================================

func TestGrpH_PHPConfigHandlers(t *testing.T) {
	t.Run("raw put nil manager 501", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("PUT", "/api/v1/php/8.2/config/raw",
			bytes.NewReader([]byte(`{"content":"x=1"}`)))
		r.SetPathValue("version", "8.2")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPConfigRawPut(rec, r)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d want 501", rec.Code)
		}
	})

	t.Run("raw put invalid JSON", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		r := httptest.NewRequest("PUT", "/api/v1/php/8.2/config/raw",
			bytes.NewReader([]byte("{bad")))
		r.SetPathValue("version", "8.2")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPConfigRawPut(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("domain config get nil manager 501", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("GET", "/api/v1/php/domains/example.com/config", nil)
		r.SetPathValue("domain", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPDomainConfigGet(rec, r)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d want 501", rec.Code)
		}
	})

	t.Run("domain config get not assigned 404", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		r := httptest.NewRequest("GET", "/api/v1/php/domains/example.com/config", nil)
		r.SetPathValue("domain", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPDomainConfigGet(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d want 404", rec.Code)
		}
	})

	t.Run("domain config get reseller forbidden", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		s.authMgr = newMockAuthManager()
		r := httptest.NewRequest("GET", "/api/v1/php/domains/example.com/config", nil)
		r.SetPathValue("domain", "example.com")
		r = withResellerContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPDomainConfigGet(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("domain config put nil manager 501", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("PUT", "/api/v1/php/domains/example.com/config",
			bytes.NewReader([]byte(`{"key":"memory_limit","value":"256M"}`)))
		r.SetPathValue("domain", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPDomainConfigPut(rec, r)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d want 501", rec.Code)
		}
	})

	t.Run("domain config put missing key", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		r := httptest.NewRequest("PUT", "/api/v1/php/domains/example.com/config",
			bytes.NewReader([]byte(`{"value":"256M"}`)))
		r.SetPathValue("domain", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPDomainConfigPut(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("domain config put unassigned 404", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		r := httptest.NewRequest("PUT", "/api/v1/php/domains/example.com/config",
			bytes.NewReader([]byte(`{"key":"memory_limit","value":"256M"}`)))
		r.SetPathValue("domain", "example.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handlePHPDomainConfigPut(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
		}
	})

	t.Run("domain config get/put round trip", func(t *testing.T) {
		s := testServer()
		s.phpMgr = phpmanager.New(logger.New("error", "text"))
		// Register the domain so config get/put have a target.
		dir := t.TempDir()
		s.phpMgr.RegisterExistingDomain("rt.com", "8.2", "127.0.0.1:9000", dir, map[string]string{"memory_limit": "128M"})
		// GET should now find it.
		rg := httptest.NewRequest("GET", "/api/v1/php/domains/rt.com/config", nil)
		rg.SetPathValue("domain", "rt.com")
		rg = withAdminContext(rg)
		recG := httptest.NewRecorder()
		s.handlePHPDomainConfigGet(recG, rg)
		if recG.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", recG.Code, recG.Body.String())
		}
		// PUT a new override.
		rp := httptest.NewRequest("PUT", "/api/v1/php/domains/rt.com/config",
			bytes.NewReader([]byte(`{"key":"upload_max_filesize","value":"64M"}`)))
		rp.SetPathValue("domain", "rt.com")
		rp = withAdminContext(rp)
		recP := httptest.NewRecorder()
		s.handlePHPDomainConfigPut(recP, rp)
		if recP.Code != http.StatusOK {
			t.Fatalf("put status=%d body=%s", recP.Code, recP.Body.String())
		}
		got := s.phpMgr.GetDomainConfig("rt.com")
		if got["upload_max_filesize"] != "64M" {
			t.Errorf("override not applied: %v", got)
		}
	})
}

// =============================================================================
// Unknown domains: alias + dismiss
// =============================================================================

func TestGrpH_UnknownDomains(t *testing.T) {
	t.Run("alias reseller forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte(`{"domain":"example.com"}`)))
		r.SetPathValue("host", "foo.com")
		r = withResellerContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("alias invalid hostname", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/badhost/alias",
			bytes.NewReader([]byte(`{"domain":"example.com"}`)))
		r.SetPathValue("host", "bad host!!")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("alias invalid JSON", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte("{bad")))
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("alias missing target domain", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte(`{"domain":""}`)))
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("alias invalid mode", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte(`{"domain":"example.com","mode":"bogus"}`)))
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("alias invalid redirect code", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte(`{"domain":"example.com","redirect_code":418}`)))
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want 400", rec.Code)
		}
	})

	t.Run("alias target not found", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte(`{"domain":"nosuch.com"}`)))
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d want 404", rec.Code)
		}
	})

	t.Run("alias creates redirect", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/alias",
			bytes.NewReader([]byte(`{"domain":"example.com","redirect_code":301}`)))
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsAlias(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
		}
		found := false
		for _, d := range s.config.Domains {
			if canonicalDomainHostname(d.Host) == "foo.com" && d.Type == "redirect" {
				found = true
			}
		}
		if !found {
			t.Error("redirect domain for foo.com not created")
		}
	})

	t.Run("dismiss reseller forbidden", func(t *testing.T) {
		s := testServer()
		s.authMgr = newMockAuthManager()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/dismiss", nil)
		r.SetPathValue("host", "foo.com")
		r = withResellerContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsDismiss(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d want 403", rec.Code)
		}
	})

	t.Run("dismiss nil tracker", func(t *testing.T) {
		s := testServer()
		r := httptest.NewRequest("POST", "/api/v1/unknown-domains/foo.com/dismiss", nil)
		r.SetPathValue("host", "foo.com")
		r = withAdminContext(r)
		rec := httptest.NewRecorder()
		s.handleUnknownDomainsDismiss(rec, r)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d want 503", rec.Code)
		}
	})
}
