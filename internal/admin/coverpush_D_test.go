package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// grpDResetCloudflare clears the package-global cloudflareConfig and registers a
// cleanup that restores it to nil. cloudflareConfig is shared across the package,
// so each test that touches it normalizes the starting state to "not connected".
func grpDResetCloudflare(t *testing.T) {
	t.Helper()
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()
	t.Cleanup(func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	})
}

// grpDDo runs a request through a handler directly (bypassing the mux) so we can
// control the auth context applied to the request.
func grpDDo(h func(http.ResponseWriter, *http.Request), r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func grpDReq(method, target string, body []byte) *http.Request {
	if body == nil {
		return httptest.NewRequest(method, target, nil)
	}
	return httptest.NewRequest(method, target, bytes.NewReader(body))
}

// =============================================================================
// validateLocalTarget (pure helper)
// =============================================================================

func TestGrpD_ValidateLocalTarget(t *testing.T) {
	good := []string{
		"http://localhost:8080",
		"https://localhost:8443",
		"tcp://localhost:22",
		"ssh://localhost:22",
		"rdp://localhost:3389",
		"unix:/tmp/app.sock",
		"http_status:404",
		"  http://localhost:8080  ", // trimmed
	}
	for _, tgt := range good {
		if err := validateLocalTarget(tgt); err != nil {
			t.Errorf("validateLocalTarget(%q) = %v, want nil", tgt, err)
		}
	}

	bad := []string{
		"",
		"   ",
		"ftp://x",
		"localhost:8080",
		"gopher://x",
	}
	for _, tgt := range bad {
		if err := validateLocalTarget(tgt); err == nil {
			t.Errorf("validateLocalTarget(%q) = nil, want error", tgt)
		}
	}
}

// =============================================================================
// WWW / apex / canonical pure helpers
// =============================================================================

func TestGrpD_AutoWWWRedirectHost(t *testing.T) {
	cases := []struct {
		d    config.Domain
		want string
	}{
		{config.Domain{Host: "example.com", Type: "static"}, "www.example.com"},
		{config.Domain{Host: "www.example.com", Type: "static"}, ""},                 // already www
		{config.Domain{Host: "example.com", Type: "redirect"}, ""},                   // redirect type
		{config.Domain{Host: "*.example.com", Type: "static"}, ""},                   // wildcard
		{config.Domain{Host: "localhost", Type: "static"}, ""},                       // no dot
		{config.Domain{Host: "example.com:8080", Type: "static"}, ""},                // has port
		{config.Domain{Host: "", Type: "static"}, ""},                                // empty
		{config.Domain{Host: "  EXAMPLE.com.  ", Type: "static"}, "www.example.com"}, // normalized
	}
	for _, c := range cases {
		if got := autoWWWRedirectHost(c.d); got != c.want {
			t.Errorf("autoWWWRedirectHost(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestGrpD_ApexAndWWWHost(t *testing.T) {
	apex, www, ok := apexAndWWWHost("example.com")
	if !ok || apex != "example.com" || www != "www.example.com" {
		t.Errorf("apexAndWWWHost(example.com) = %q,%q,%v", apex, www, ok)
	}
	apex, www, ok = apexAndWWWHost("www.example.com")
	if !ok || apex != "example.com" || www != "www.example.com" {
		t.Errorf("apexAndWWWHost(www.example.com) = %q,%q,%v", apex, www, ok)
	}
	if _, _, ok := apexAndWWWHost("localhost"); ok {
		t.Error("apexAndWWWHost(localhost) ok=true, want false")
	}
	if _, _, ok := apexAndWWWHost("*.example.com"); ok {
		t.Error("apexAndWWWHost(*.example.com) ok=true, want false")
	}
	if _, _, ok := apexAndWWWHost(""); ok {
		t.Error("apexAndWWWHost(empty) ok=true, want false")
	}
	if _, _, ok := apexAndWWWHost("www.com"); ok {
		t.Error("apexAndWWWHost(www.com) ok=true, want false (apex has no dot)")
	}
}

func TestGrpD_MainDomainHostname(t *testing.T) {
	// apex preference (default)
	if got := mainDomainHostname(config.Domain{Host: "www.example.com", Type: "static"}); got != "example.com" {
		t.Errorf("mainDomainHostname apex = %q, want example.com", got)
	}
	// www preference
	d := config.Domain{Host: "example.com", Type: "static", CanonicalHost: "www"}
	if got := mainDomainHostname(d); got != "www.example.com" {
		t.Errorf("mainDomainHostname www = %q, want www.example.com", got)
	}
	// redirect type ignores www preference
	dr := config.Domain{Host: "example.com", Type: "redirect", CanonicalHost: "www"}
	if got := mainDomainHostname(dr); got != "example.com" {
		t.Errorf("mainDomainHostname redirect = %q, want example.com", got)
	}
	// empty host falls back to normalized
	if got := mainDomainHostname(config.Domain{Host: "  ", Type: "static"}); got != "" {
		t.Errorf("mainDomainHostname empty = %q, want empty", got)
	}
}

func TestGrpD_PublicDomainAliases(t *testing.T) {
	d := config.Domain{
		Host: "example.com",
		Aliases: []string{
			"shop.example.com",
			"shop.example.com", // dup
			"example.com",      // == host, excluded
			"www.example.com",  // canonicalizes to example.com == host, excluded
			"",                 // empty
			"blog.example.com",
		},
	}
	got := publicDomainAliases(d)
	want := []string{"shop.example.com", "blog.example.com"}
	if len(got) != len(want) {
		t.Fatalf("publicDomainAliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("publicDomainAliases[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGrpD_IsImplicitWWWRedirectForDomains(t *testing.T) {
	primary := config.Domain{Host: "example.com", Type: "static"}
	wwwRedirect := newCanonicalRedirectAliasDomain("www.example.com", "example.com", 0, true)
	domains := []config.Domain{primary, wwwRedirect}

	if !isImplicitWWWRedirectForDomains(wwwRedirect, domains) {
		t.Error("expected www redirect to be recognized as implicit www redirect")
	}
	// A non-redirect domain is never an implicit www redirect.
	if isImplicitWWWRedirectForDomains(primary, domains) {
		t.Error("primary static domain reported as implicit www redirect")
	}
	// A redirect with no matching primary is not implicit.
	orphan := newCanonicalRedirectAliasDomain("www.other.com", "other.com", 0, true)
	if isImplicitWWWRedirectForDomains(orphan, domains) {
		t.Error("orphan redirect reported as implicit www redirect")
	}
	// Empty host redirect is not implicit.
	empty := config.Domain{Host: "", Type: "redirect"}
	if isImplicitWWWRedirectForDomains(empty, domains) {
		t.Error("empty-host redirect reported as implicit www redirect")
	}
}

func TestGrpD_UpsertCanonicalRedirectAliasDomains(t *testing.T) {
	// Append path: alias not present yet. Use a non-www alias because
	// canonicalDomainHostname strips a leading "www." (collapsing it to the
	// apex), so a www alias would not be stored verbatim.
	domains := []config.Domain{{Host: "example.com", Type: "static"}}
	upsertCanonicalRedirectAliasDomains(&domains, 0, []string{"aka.example.com"}, "example.com", 0, true)
	if len(domains) != 2 {
		t.Fatalf("after upsert append: len=%d, want 2", len(domains))
	}
	if domains[1].Host != "aka.example.com" || domains[1].Type != string(config.DomainTypeRedirect) {
		t.Errorf("appended alias = %+v", domains[1])
	}

	// Update path: alias already present (as a plain domain) gets replaced.
	domains2 := []config.Domain{
		{Host: "example.com", Type: "static"},
		{Host: "aka.example.com", Type: "static"},
	}
	upsertCanonicalRedirectAliasDomains(&domains2, 0, []string{"aka.example.com"}, "example.com", 301, false)
	if len(domains2) != 2 {
		t.Fatalf("after upsert update: len=%d, want 2", len(domains2))
	}
	if domains2[1].Type != string(config.DomainTypeRedirect) {
		t.Errorf("alias not converted to redirect: %+v", domains2[1])
	}

	// Empty/whitespace aliases are skipped.
	domains3 := []config.Domain{{Host: "example.com", Type: "static"}}
	upsertCanonicalRedirectAliasDomains(&domains3, 0, []string{"", "   "}, "example.com", 0, true)
	if len(domains3) != 1 {
		t.Errorf("empty aliases should be skipped, len=%d", len(domains3))
	}
}

// =============================================================================
// handleFeatures
// =============================================================================

func TestGrpD_HandleFeatures(t *testing.T) {
	s := testServer()
	rec := grpDDo(s.handleFeatures, grpDReq("GET", "/api/v1/features", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handleFeatures status = %d, want 200", rec.Code)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("handleFeatures body not a JSON object: %v", err)
	}
	if _, ok := out["apps"]; !ok {
		t.Error("expected 'apps' feature key in response")
	}
}

// =============================================================================
// Cloudflare status / IPs (no auth gate on GET)
// =============================================================================

func TestGrpD_HandleCloudflareStatusNotConnected(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	rec := grpDDo(s.handleCloudflareStatus, withAdminContext(grpDReq("GET", "/cf/status", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["connected"] != false {
		t.Errorf("connected = %v, want false", out["connected"])
	}
}

func TestGrpD_HandleCloudflareStatusConnected(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "secret-token-1234", AccountID: "acct-1", Email: "a@b.com",
		Connected: true, Tunnels: []cloudflareTunnel{},
	}
	cloudflareMu.Unlock()
	s := testServer()
	rec := grpDDo(s.handleCloudflareStatus, withAdminContext(grpDReq("GET", "/cf/status", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["connected"] != true {
		t.Errorf("connected = %v, want true", out["connected"])
	}
	if out["account_id"] != "acct-1" {
		t.Errorf("account_id = %v", out["account_id"])
	}
}

func TestGrpD_HandleCloudflareIPs(t *testing.T) {
	s := testServer()
	s.config.Global.Cloudflare.IPRanges = []string{"173.245.48.0/20"}
	rec := grpDDo(s.handleCloudflareIPs, grpDReq("GET", "/cf/ips", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", out["count"])
	}
}

// =============================================================================
// handleCloudflareIPsUpdate (admin-gated, NormalizeCIDRs is local)
// =============================================================================

func TestGrpD_HandleCloudflareIPsUpdateReseller403(t *testing.T) {
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/ips", []byte(`{"ip_ranges":["173.245.48.0/20"]}`)))
	rec := grpDDo(s.handleCloudflareIPsUpdate, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareIPsUpdateInvalidJSON(t *testing.T) {
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/ips", []byte(`{not json`)))
	rec := grpDDo(s.handleCloudflareIPsUpdate, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGrpD_HandleCloudflareIPsUpdateInvalidCIDR(t *testing.T) {
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/ips", []byte(`{"ip_ranges":["notacidr"]}`)))
	rec := grpDDo(s.handleCloudflareIPsUpdate, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGrpD_HandleCloudflareIPsUpdateValid(t *testing.T) {
	s, _ := testServerWithRoot(t)
	// Give the server a config path so persistConfig has somewhere to write.
	s.configPath = filepath.Join(t.TempDir(), "uwas.yaml")
	r := withAdminContext(grpDReq("POST", "/cf/ips", []byte(`{"ip_ranges":["173.245.48.0/20"]}`)))
	rec := grpDDo(s.handleCloudflareIPsUpdate, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != "updated" {
		t.Errorf("status field = %v, want updated", out["status"])
	}
	if out["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", out["count"])
	}
}

// =============================================================================
// handleCloudflareIPsSync / connect: only auth/JSON gating (no network calls)
// =============================================================================

func TestGrpD_HandleCloudflareIPsSyncReseller403(t *testing.T) {
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/ips/sync", nil))
	rec := grpDDo(s.handleCloudflareIPsSync, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareConnectReseller403(t *testing.T) {
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/connect", []byte(`{"token":"t","account_id":"a"}`)))
	rec := grpDDo(s.handleCloudflareConnect, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareConnectInvalidJSON(t *testing.T) {
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/connect", []byte(`{bad`)))
	rec := grpDDo(s.handleCloudflareConnect, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGrpD_HandleCloudflareConnectMissingToken(t *testing.T) {
	s := testServer()
	// Missing token/account_id -> 400 before any network validation.
	r := withAdminContext(grpDReq("POST", "/cf/connect", []byte(`{"token":"","account_id":""}`)))
	rec := grpDDo(s.handleCloudflareConnect, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Tunnel handlers: auth + not-connected + invalid JSON early branches
// =============================================================================

func TestGrpD_HandleCloudflareTunnelCreateReseller403(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/tunnels", []byte(`{}`)))
	rec := grpDDo(s.handleCloudflareTunnelCreate, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelCreateNotConnected(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/tunnels", []byte(`{"name":"x"}`)))
	rec := grpDDo(s.handleCloudflareTunnelCreate, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (not connected)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelCreateInvalidJSON(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Connected: true, Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/tunnels", []byte(`{bad json`)))
	rec := grpDDo(s.handleCloudflareTunnelCreate, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid JSON)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelDeleteReseller403(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withResellerContext(grpDReq("DELETE", "/cf/tunnels/abc", nil))
	r.SetPathValue("id", "abc")
	rec := grpDDo(s.handleCloudflareTunnelDelete, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelDeleteMissingID(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("DELETE", "/cf/tunnels/", nil))
	rec := grpDDo(s.handleCloudflareTunnelDelete, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing id)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelDeleteNotConnected(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("DELETE", "/cf/tunnels/abc", nil))
	r.SetPathValue("id", "abc")
	rec := grpDDo(s.handleCloudflareTunnelDelete, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (not connected)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelStartReseller403(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/tunnels/abc/start", nil))
	r.SetPathValue("id", "abc")
	rec := grpDDo(s.handleCloudflareTunnelStart, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelStartNotFound(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/tunnels/missing/start", nil))
	r.SetPathValue("id", "missing")
	rec := grpDDo(s.handleCloudflareTunnelStart, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (tunnel not found)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelStopReseller403(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/tunnels/abc/stop", nil))
	r.SetPathValue("id", "abc")
	rec := grpDDo(s.handleCloudflareTunnelStop, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelStopMissingID(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/tunnels//stop", nil))
	rec := grpDDo(s.handleCloudflareTunnelStop, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing id)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelStopNotFound(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/tunnels/missing/stop", nil))
	r.SetPathValue("id", "missing")
	rec := grpDDo(s.handleCloudflareTunnelStop, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (tunnel not found)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelLogsNotFound(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("GET", "/cf/tunnels/missing/logs", nil))
	r.SetPathValue("id", "missing")
	rec := grpDDo(s.handleCloudflareTunnelLogs, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (tunnel not found)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareTunnelLogsRunnerNil(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Connected: true,
		Tunnels:   []cloudflareTunnel{{ID: "t1", Name: "t1"}},
	}
	cloudflareMu.Unlock()
	s := testServer() // cfRunner is nil
	r := withAdminContext(grpDReq("GET", "/cf/tunnels/t1/logs", nil))
	r.SetPathValue("id", "t1")
	rec := grpDDo(s.handleCloudflareTunnelLogs, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty logs)", rec.Code)
	}
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["logs"] != "" {
		t.Errorf("logs = %q, want empty", out["logs"])
	}
}

// =============================================================================
// handleCloudflaredInstall: only reseller 403 (admin path would shell out)
// =============================================================================

func TestGrpD_HandleCloudflaredInstallReseller403(t *testing.T) {
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/cloudflared/install", nil))
	rec := grpDDo(s.handleCloudflaredInstall, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// =============================================================================
// handleCloudflareCachePurge: not-connected + invalid JSON early branches
// =============================================================================

func TestGrpD_HandleCloudflareCachePurgeNotConnected(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/cache/purge", []byte(`{"everything":true}`)))
	rec := grpDDo(s.handleCloudflareCachePurge, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (not connected)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareCachePurgeInvalidJSON(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Connected: true, Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/cache/purge", []byte(`{bad`)))
	rec := grpDDo(s.handleCloudflareCachePurge, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid JSON)", rec.Code)
	}
}

// =============================================================================
// handleCloudflareZones / handleCloudflareZoneImport: not-connected / reseller
// =============================================================================

func TestGrpD_HandleCloudflareZonesNotConnected(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	rec := grpDDo(s.handleCloudflareZones, withAdminContext(grpDReq("GET", "/cf/zones", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Not connected returns an empty JSON array.
	var arr []any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("body not an array: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("zones = %v, want empty", arr)
	}
}

func TestGrpD_HandleCloudflareZoneImportReseller403(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withResellerContext(grpDReq("POST", "/cf/zones/z1/import", []byte(`{}`)))
	r.SetPathValue("id", "z1")
	rec := grpDDo(s.handleCloudflareZoneImport, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGrpD_HandleCloudflareZoneImportMissingID(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/zones//import", []byte(`{}`)))
	rec := grpDDo(s.handleCloudflareZoneImport, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing zone id)", rec.Code)
	}
}

func TestGrpD_HandleCloudflareZoneImportNotConnected(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	r := withAdminContext(grpDReq("POST", "/cf/zones/z1/import", []byte(`{}`)))
	r.SetPathValue("id", "z1")
	rec := grpDDo(s.handleCloudflareZoneImport, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (not connected)", rec.Code)
	}
}

// =============================================================================
// loadCloudflareState / saveCloudflareStateLocked (disk round-trip via temp dir)
// =============================================================================

func TestGrpD_CloudflareStateFileNoConfigPath(t *testing.T) {
	s := testServer() // configPath == ""
	if got := s.cloudflareStateFile(); got != "" {
		t.Errorf("cloudflareStateFile() = %q, want empty when no configPath", got)
	}
	// load is a no-op (no error) when there is no path.
	if err := s.loadCloudflareState(); err != nil {
		t.Errorf("loadCloudflareState() with no path = %v, want nil", err)
	}
}

func TestGrpD_SaveAndLoadCloudflareStateRoundTrip(t *testing.T) {
	grpDResetCloudflare(t)
	dir := t.TempDir()
	s := testServer()
	s.configPath = filepath.Join(dir, "uwas.yaml")

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "tok", AccountID: "acct", Email: "e@x.com",
		Connected: true,
		Tunnels:   []cloudflareTunnel{{ID: "t1", Name: "t1", Hostname: "a.example.com"}},
	}
	saveErr := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if saveErr != nil {
		t.Fatalf("saveCloudflareStateLocked() = %v", saveErr)
	}

	statePath := filepath.Join(dir, "cloudflare.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	// Clear and reload from disk.
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()
	if err := s.loadCloudflareState(); err != nil {
		t.Fatalf("loadCloudflareState() = %v", err)
	}
	cloudflareMu.RLock()
	loaded := cloudflareConfig
	cloudflareMu.RUnlock()
	if loaded == nil || loaded.AccountID != "acct" || len(loaded.Tunnels) != 1 {
		t.Fatalf("loaded state mismatch: %+v", loaded)
	}
	if loaded.SchemaVersion != cloudflareStateSchemaCurrent {
		t.Errorf("schema version = %d, want %d", loaded.SchemaVersion, cloudflareStateSchemaCurrent)
	}
}

func TestGrpD_SaveCloudflareStateNilRemovesFile(t *testing.T) {
	grpDResetCloudflare(t)
	dir := t.TempDir()
	s := testServer()
	s.configPath = filepath.Join(dir, "uwas.yaml")

	// Write a state, then clear it; save should remove the file.
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "x", Connected: true, Tunnels: []cloudflareTunnel{}}
	_ = s.saveCloudflareStateLocked()
	cloudflareConfig = nil
	saveErr := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if saveErr != nil {
		t.Fatalf("saveCloudflareStateLocked(nil) = %v", saveErr)
	}
	statePath := filepath.Join(dir, "cloudflare.json")
	if _, err := os.Stat(statePath); err == nil {
		t.Errorf("state file still exists after nil save")
	}
}
