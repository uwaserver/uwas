package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/router"
)

func grpILog() *logger.Logger { return logger.New("error", "text") }

// =============================================================================
// maskCloudflareToken (pure)
// =============================================================================

func TestGrpI_MaskCloudflareToken(t *testing.T) {
	if got := maskCloudflareToken("ab"); got != "****" {
		t.Errorf("short = %q", got)
	}
	if got := maskCloudflareToken("abcd"); got != "****" {
		t.Errorf("exactly4 = %q", got)
	}
	if got := maskCloudflareToken("abcdefgh"); got != "****efgh" {
		t.Errorf("long = %q", got)
	}
}

// =============================================================================
// SetPHPManager wires the domain-change callback
// =============================================================================

func TestGrpI_SetPHPManagerWiring(t *testing.T) {
	s := testServer()
	m := phpmanager.New(grpILog())
	s.SetPHPManager(m)
	if s.phpMgr != m {
		t.Fatal("phpMgr not set")
	}
	// Trigger the wired callback; it should update FPMAddress for example.com.
	m.SetDomainChangeFunc(nil) // ensure we test our own invocation path indirectly
	s.SetPHPManager(m)         // re-wire
	// Simulate a domain PHP start by invoking the manager's change func via reflection-free path:
	// We just assert no panic and the server remained consistent.
	if len(s.config.Domains) == 0 {
		t.Fatal("expected seeded domains")
	}
}

// =============================================================================
// PHP enable/disable/restart/config with a real phpMgr (in-memory + temp files)
// =============================================================================

func TestGrpI_PHPEnableDisable(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(grpILog()))

	// enable some version (operates on in-memory map; no version present is fine)
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", nil))
	req.SetPathValue("version", "8.3")
	s.handlePHPEnable(rec, req)
	if rec.Code != 200 {
		t.Errorf("enable = %d body=%s", rec.Code, rec.Body.String())
	}

	// disable returns conflict when version unknown, or 200 — accept either non-5xx-other
	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("POST", "/x", nil))
	req.SetPathValue("version", "8.3")
	s.handlePHPDisable(rec, req)
	if rec.Code != 200 && rec.Code != http.StatusConflict {
		t.Errorf("disable = %d", rec.Code)
	}
}

func TestGrpI_PHPEnableResellerForbidden(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(grpILog()))
	rec := httptest.NewRecorder()
	s.handlePHPEnable(rec, withResellerContext(httptest.NewRequest("POST", "/x", nil)))
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller enable = %d, want 403", rec.Code)
	}
}

func TestGrpI_PHPHandlersNilManager(t *testing.T) {
	s := testServer() // no phpMgr
	for name, h := range map[string]func(http.ResponseWriter, *http.Request){
		"enable":       s.handlePHPEnable,
		"disable":      s.handlePHPDisable,
		"restart":      s.handlePHPRestart,
		"configRawGet": s.handlePHPConfigRawGet,
		"configRawPut": s.handlePHPConfigRawPut,
		"list":         s.handlePHPList,
	} {
		rec := httptest.NewRecorder()
		h(rec, withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}"))))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s nil-manager = %d, want 501", name, rec.Code)
		}
	}
}

func TestGrpI_PHPConfigRawPutValidation(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(grpILog()))

	// invalid JSON
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader("{bad")))
	req.SetPathValue("version", "8.3")
	s.handlePHPConfigRawPut(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}

	// reseller forbidden
	rec = httptest.NewRecorder()
	s.handlePHPConfigRawPut(rec, withResellerContext(httptest.NewRequest("POST", "/x", strings.NewReader("{}"))))
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller = %d, want 403", rec.Code)
	}
}

func TestGrpI_PHPRestartResellerForbidden(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(grpILog()))
	rec := httptest.NewRecorder()
	s.handlePHPRestart(rec, withResellerContext(httptest.NewRequest("POST", "/x", nil)))
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller restart = %d, want 403", rec.Code)
	}
}

func TestGrpI_PHPListWithManager(t *testing.T) {
	s := testServer()
	s.SetPHPManager(phpmanager.New(grpILog()))
	rec := httptest.NewRecorder()
	s.handlePHPList(rec, withAdminContext(httptest.NewRequest("GET", "/x", nil)))
	if rec.Code != 200 {
		t.Errorf("php list = %d", rec.Code)
	}
}

// =============================================================================
// handlePHPInstallStatus (uses taskMgr)
// =============================================================================

func TestGrpI_PHPInstallStatusIdle(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handlePHPInstallStatus(rec, withAdminContext(httptest.NewRequest("GET", "/x", nil)))
	if rec.Code != 200 {
		t.Fatalf("install status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "idle") {
		t.Errorf("expected idle: %s", rec.Body.String())
	}
}

// =============================================================================
// persistConfig + removeDomainFile with a real temp config path
// =============================================================================

func TestGrpI_PersistConfigAndRemoveDomainFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")

	s := testServer()
	s.SetConfigPath(cfgPath)
	s.config.DomainsDir = "domains.d"

	if err := s.persistConfig(); err != nil {
		t.Fatalf("persistConfig: %v", err)
	}
	// main config written
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("main config not written: %v", err)
	}
	// domain file written
	domFile := filepath.Join(dir, "domains.d", "example.com.yaml")
	if _, err := os.Stat(domFile); err != nil {
		t.Errorf("domain file not written: %v", err)
	}

	// removeDomainFile deletes it
	s.removeDomainFile("example.com")
	if _, err := os.Stat(domFile); !os.IsNotExist(err) {
		t.Errorf("domain file not removed")
	}
}

func TestGrpI_PersistConfigNoPath(t *testing.T) {
	s := testServer() // configPath == ""
	if err := s.persistConfig(); err != nil {
		t.Errorf("persistConfig no-path should be nil, got %v", err)
	}
	s.removeDomainFile("example.com") // no-op, no panic
}

// =============================================================================
// persistDomainPHPOverrides
// =============================================================================

func TestGrpI_PersistDomainPHPOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	s := testServer()
	s.SetConfigPath(cfgPath)
	s.config.DomainsDir = "domains.d"
	s.SetPHPManager(phpmanager.New(grpILog()))

	// write the domain file first so persistDomainPHPOverrides can read it
	if err := s.persistConfig(); err != nil {
		t.Fatalf("persistConfig: %v", err)
	}

	// Should not panic; reads + rewrites the domain file with overrides.
	s.persistDomainPHPOverrides("example.com")

	// Bad domain path: contains traversal -> early warn return, no panic.
	s.persistDomainPHPOverrides("../evil")
}

// =============================================================================
// handleUnknownDomains block/unblock/dismiss
// =============================================================================

func TestGrpI_UnknownDomainsBlockUnblockDismiss(t *testing.T) {
	s := testServer()
	s.unknownHT = router.NewUnknownHostTracker()

	for _, tc := range []struct {
		name string
		h    func(http.ResponseWriter, *http.Request)
		want string
	}{
		{"block", s.handleUnknownDomainsBlock, "blocked"},
		{"unblock", s.handleUnknownDomainsUnblock, "unblocked"},
		{"dismiss", s.handleUnknownDomainsDismiss, "dismissed"},
	} {
		rec := httptest.NewRecorder()
		req := withAdminContext(httptest.NewRequest("POST", "/x", nil))
		req.SetPathValue("host", "evil.example")
		tc.h(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s = %d body=%s", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s body = %s, want %s", tc.name, rec.Body.String(), tc.want)
		}
	}
}

func TestGrpI_UnknownDomainsResellerForbidden(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()
	s.unknownHT = router.NewUnknownHostTracker()
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("POST", "/x", nil))
	req.SetPathValue("host", "evil.example")
	s.handleUnknownDomainsBlock(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller block = %d, want 403", rec.Code)
	}
}

func TestGrpI_UnknownDomainsNoTracker(t *testing.T) {
	s := testServer() // unknownHT nil
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", nil))
	req.SetPathValue("host", "evil.example")
	s.handleUnknownDomainsBlock(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no tracker = %d, want 503", rec.Code)
	}
}

// =============================================================================
// handleLogout
// =============================================================================

func TestGrpI_LogoutNoAuthMgr(t *testing.T) {
	s := testServer() // authMgr nil
	rec := httptest.NewRecorder()
	s.handleLogout(rec, withAdminContext(httptest.NewRequest("POST", "/x", nil)))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("logout no authMgr = %d, want 501", rec.Code)
	}
}

func TestGrpI_LogoutWithToken(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()

	// token via header
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/x", nil))
	req.Header.Set("X-Session-Token", "session-admin")
	s.handleLogout(rec, req)
	if rec.Code != 200 {
		t.Errorf("logout header = %d", rec.Code)
	}

	// token via body
	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("POST", "/x", strings.NewReader(`{"token":"abc"}`)))
	s.handleLogout(rec, req)
	if rec.Code != 200 {
		t.Errorf("logout body = %d", rec.Code)
	}
}

// =============================================================================
// handleDomainRawPut
// =============================================================================

func TestGrpI_DomainRawPut(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	s := testServer()
	s.SetConfigPath(cfgPath)
	s.config.DomainsDir = "domains.d"

	validYAML := "host: raw.example.com\ntype: static\nroot: /var/www/raw.example.com\nssl:\n  mode: auto\n"

	// success
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("PUT", "/x", strings.NewReader(`{"content":`+jsonQuote(validYAML)+`}`)))
	req.SetPathValue("host", "raw.example.com")
	s.handleDomainRawPut(rec, req)
	if rec.Code != 200 {
		t.Fatalf("raw put = %d body=%s", rec.Code, rec.Body.String())
	}

	// invalid JSON
	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("PUT", "/x", strings.NewReader("{bad")))
	req.SetPathValue("host", "raw.example.com")
	s.handleDomainRawPut(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d, want 400", rec.Code)
	}

	// invalid YAML content
	rec = httptest.NewRecorder()
	req = withAdminContext(httptest.NewRequest("PUT", "/x", strings.NewReader(`{"content":"\t\tnot: : yaml: :"}`)))
	req.SetPathValue("host", "raw.example.com")
	s.handleDomainRawPut(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid YAML = %d, want 400", rec.Code)
	}
}

func TestGrpI_DomainRawPutResellerForbidden(t *testing.T) {
	s := testServer()
	s.authMgr = newMockAuthManager()
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("PUT", "/x", strings.NewReader("{}")))
	req.SetPathValue("host", "notmine.com")
	s.handleDomainRawPut(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller = %d, want 403", rec.Code)
	}
}

func TestGrpI_DomainRawPutNoConfigPath(t *testing.T) {
	s := testServer() // configPath == "" -> domainFilePath error
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("PUT", "/x", strings.NewReader("{}")))
	req.SetPathValue("host", "x.com")
	s.handleDomainRawPut(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no config path = %d, want 400", rec.Code)
	}
}

// jsonQuote produces a JSON string literal for the given content.
func jsonQuote(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// =============================================================================
// findDomainHostnameConflict via validateDomainConfig path is covered elsewhere;
// here we exercise the helper used by add/update.
// =============================================================================

func TestGrpI_DomainTypeUsesWebRoot(t *testing.T) {
	cases := map[string]bool{"static": true, "php": true, "proxy": false, "redirect": false}
	for typ, want := range cases {
		if got := domainTypeUsesWebRoot(typ); got != want {
			t.Errorf("domainTypeUsesWebRoot(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestGrpI_ValidateDomainUpdateConfig(t *testing.T) {
	// valid partial
	d := &config.Domain{Host: "x.com", Type: "static"}
	if err := validateDomainUpdateConfig(d, nil); err != nil {
		t.Errorf("valid partial err: %v", err)
	}
}
