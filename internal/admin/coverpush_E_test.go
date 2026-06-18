package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/migrate"
)

// grpEWPRequest builds a request with a "domain" path value and the given body.
func grpEWPRequest(method, domain, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/v1/wordpress/"+domain, nil)
	} else {
		r = httptest.NewRequest(method, "/api/v1/wordpress/"+domain, strings.NewReader(body))
	}
	r.SetPathValue("domain", domain)
	return r
}

// grpEResellerServer returns a server with a mock auth manager so that
// reseller access checks actually run (reseller can only manage reseller.com).
func grpEResellerServer() *Server {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	return s
}

// =============================================================================
// WordPress handlers — reseller 403 (domain-scoped)
// =============================================================================

func TestGrpE_WP_ResellerForbidden(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		body    string
		handler func(*Server) func(http.ResponseWriter, *http.Request)
	}{
		{"site_detail", "GET", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPSiteDetail }},
		{"update_core", "POST", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPUpdateCore }},
		{"reinstall", "POST", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPReinstall }},
		{"users", "GET", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPUsers }},
		{"change_password", "POST", `{"username":"u","password":"longpassword"}`, func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPChangePassword }},
		{"harden", "POST", `{}`, func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPHarden }},
		{"optimize_db", "POST", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPOptimizeDB }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := grpEResellerServer()
			rec := httptest.NewRecorder()
			req := withResellerContext(grpEWPRequest(tc.method, "example.com", tc.body))
			tc.handler(s)(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// =============================================================================
// WordPress handlers — "not a WordPress site" branch (admin, empty root)
// =============================================================================

func TestGrpE_WP_NotAWordPressSite(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		body    string
		handler func(*Server) func(http.ResponseWriter, *http.Request)
	}{
		{"site_detail", "GET", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPSiteDetail }},
		{"update_core", "POST", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPUpdateCore }},
		{"reinstall", "POST", "", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleWPReinstall }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServerWithRoot(t)
			rec := httptest.NewRecorder()
			req := withAdminContext(grpEWPRequest(tc.method, "example.com", tc.body))
			tc.handler(s)(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "not a WordPress site") {
				t.Fatalf("body = %s, want 'not a WordPress site'", rec.Body.String())
			}
		})
	}
}

// =============================================================================
// WordPress handlers — invalid JSON / missing field validation
// =============================================================================

func TestGrpE_WP_ChangePasswordValidation(t *testing.T) {
	s, _ := testServerWithRoot(t)

	// invalid JSON
	rec := httptest.NewRecorder()
	s.handleWPChangePassword(rec, withAdminContext(grpEWPRequest("POST", "example.com", "not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", rec.Code)
	}

	// missing username/password
	rec = httptest.NewRecorder()
	s.handleWPChangePassword(rec, withAdminContext(grpEWPRequest("POST", "example.com", `{"username":"u"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing field status = %d, want 400", rec.Code)
	}

	// too-short password
	rec = httptest.NewRecorder()
	s.handleWPChangePassword(rec, withAdminContext(grpEWPRequest("POST", "example.com", `{"username":"u","password":"short"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400", rec.Code)
	}
}

func TestGrpE_WP_HardenInvalidJSON(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	s.handleWPHarden(rec, withAdminContext(grpEWPRequest("POST", "example.com", "not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_WP_PluginActionInvalid(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/example.com/plugin/foo/bogus", nil)
	req.SetPathValue("domain", "example.com")
	req.SetPathValue("plugin", "foo")
	req.SetPathValue("action", "bogus")
	s.handleWPPluginAction(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid action") {
		t.Fatalf("body = %s, want 'invalid action'", rec.Body.String())
	}
}

func TestGrpE_WP_PluginActionResellerForbidden(t *testing.T) {
	s := grpEResellerServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/wordpress/example.com/plugin/foo/activate", nil)
	req.SetPathValue("domain", "example.com")
	req.SetPathValue("plugin", "foo")
	req.SetPathValue("action", "activate")
	s.handleWPPluginAction(rec, withResellerContext(req))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Software library handlers — auth gating, invalid JSON, not-found
// =============================================================================

func grpESoftwareReq(method, name, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/v1/software/"+name, nil)
	} else {
		r = httptest.NewRequest(method, "/api/v1/software/"+name, strings.NewReader(body))
	}
	r.SetPathValue("name", name)
	return r
}

func TestGrpE_Software_ResellerForbidden(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		handler func(*Server) func(http.ResponseWriter, *http.Request)
	}{
		{"connect", "POST", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareDomainConnect }},
		{"disconnect", "DELETE", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareDomainDisconnect }},
		{"backup_list", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareBackupList }},
		{"backup_delete", "DELETE", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareBackupDelete }},
		{"logs", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareLogs }},
		{"processes", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareProcesses }},
		{"monitor", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareMonitor }},
		{"update", "POST", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareUpdate }},
		{"backup", "POST", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareBackup }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := grpEResellerServer()
			rec := httptest.NewRecorder()
			req := withResellerContext(grpESoftwareReq(tc.method, "anything", ""))
			tc.handler(s)(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGrpE_Software_NotFound(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	cases := []struct {
		name    string
		method  string
		handler func(*Server) func(http.ResponseWriter, *http.Request)
	}{
		{"connect", "POST", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareDomainConnect }},
		{"disconnect", "DELETE", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareDomainDisconnect }},
		{"backup_list", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareBackupList }},
		{"backup_delete", "DELETE", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareBackupDelete }},
		{"logs", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareLogs }},
		{"processes", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareProcesses }},
		{"monitor", "GET", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareMonitor }},
		{"update", "POST", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareUpdate }},
		{"backup", "POST", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareBackup }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer()
			rec := httptest.NewRecorder()
			req := withAdminContext(grpESoftwareReq(tc.method, "does-not-exist", ""))
			tc.handler(s)(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGrpE_Software_DomainConnectInvalidJSON(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	dir := filepath.Join(root, "web")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "web",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-web",
		HasWeb:      true,
		HostPort:    3001,
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSoftwareDomainConnect(rec, withAdminContext(grpESoftwareReq("POST", "web", "not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}

	// invalid domain
	rec = httptest.NewRecorder()
	s.handleSoftwareDomainConnect(rec, withAdminContext(grpESoftwareReq("POST", "web", `{"domain":"!!bad!!"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid domain status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_Software_DomainConnectNoWeb(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	dir := filepath.Join(root, "cache")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "cache",
		TemplateID:  "redis",
		Template:    "Redis",
		Category:    "Infrastructure",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-cache",
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSoftwareDomainConnect(rec, withAdminContext(grpESoftwareReq("POST", "cache", `{"domain":"x.example.com"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no web service") {
		t.Fatalf("body = %s, want 'no web service'", rec.Body.String())
	}
}

func TestGrpE_Software_DomainDisconnectNoDomain(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	dir := filepath.Join(root, "web")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "web",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-web",
		HasWeb:      true,
		HostPort:    3001,
		// no Domain attached
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSoftwareDomainDisconnect(rec, withAdminContext(grpESoftwareReq("DELETE", "web", "")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no-op disconnect), body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_Software_BackupDeleteInvalidName(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	dir := filepath.Join(root, "web")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	inst := softwareInstance{
		Name:        "web",
		TemplateID:  "uptime-kuma",
		Template:    "Uptime Kuma",
		Category:    "Monitoring",
		Dir:         dir,
		ComposeFile: filepath.Join(dir, "docker-compose.yml"),
		Project:     "uwas-web",
		HasWeb:      true,
		HostPort:    3001,
	}
	if err := saveSoftwareInstance(inst); err != nil {
		t.Fatal(err)
	}

	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/software/web/backups/../escape", nil)
	req.SetPathValue("name", "web")
	req.SetPathValue("backup", "../escape")
	s.handleSoftwareBackupDelete(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_Software_ComposeActionNotFound(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	s := testServer()
	rec := httptest.NewRecorder()
	req := grpESoftwareReq("POST", "missing", "")
	s.handleSoftwareComposeAction(rec, withAdminContext(req), "start", []string{"up", "-d"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_Software_StartStopResellerForbidden(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(*Server) func(http.ResponseWriter, *http.Request)
	}{
		{"start", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareStart }},
		{"stop", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareStop }},
		{"restart", func(s *Server) func(http.ResponseWriter, *http.Request) { return s.handleSoftwareRestart }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := grpEResellerServer()
			rec := httptest.NewRecorder()
			tc.handler(s)(rec, withResellerContext(grpESoftwareReq("POST", "x", "")))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// =============================================================================
// Software pure helpers
// =============================================================================

func TestGrpE_PortFromLocalHTTPURL(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"http://127.0.0.1:8080", 8080},
		{"127.0.0.1:9090", 9090},
		{"http://localhost", 80},
		{"https://localhost", 443},
		{"https://127.0.0.1", 443},
		{"http://example.com:8080", 0}, // not a local host
		{"ftp://127.0.0.1", 0},         // unknown scheme, no port
		{"http://127.0.0.1:99999", 80}, // invalid port → falls through to scheme default
	}
	for _, tc := range cases {
		if got := portFromLocalHTTPURL(tc.raw); got != tc.want {
			t.Errorf("portFromLocalHTTPURL(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestGrpE_AllocateSoftwarePort(t *testing.T) {
	root := t.TempDir()
	origRoot := softwareLibraryRoot
	softwareLibraryRoot = root
	t.Cleanup(func() { softwareLibraryRoot = origRoot })

	origAvail := softwarePortAvailable
	t.Cleanup(func() { softwarePortAvailable = origAvail })

	s := testServer()

	// Only port 4005 is free.
	softwarePortAvailable = func(port int) bool { return port == 4005 }
	if got := s.allocateSoftwarePort(4000); got != 4005 {
		t.Fatalf("allocateSoftwarePort(4000) = %d, want 4005", got)
	}

	// Out-of-range start is normalized to 3001 base; only 3001 free.
	softwarePortAvailable = func(port int) bool { return port == 3001 }
	if got := s.allocateSoftwarePort(-1); got != 3001 {
		t.Fatalf("allocateSoftwarePort(-1) = %d, want 3001", got)
	}

	// Nothing free → 0.
	softwarePortAvailable = func(port int) bool { return false }
	if got := s.allocateSoftwarePort(5000); got != 0 {
		t.Fatalf("allocateSoftwarePort(5000) with none free = %d, want 0", got)
	}
}

func TestGrpE_SoftwarePortAvailableRange(t *testing.T) {
	// The default softwarePortAvailable rejects out-of-range ports.
	if softwarePortAvailable(0) {
		t.Error("port 0 should be unavailable")
	}
	if softwarePortAvailable(70000) {
		t.Error("port 70000 should be unavailable")
	}
}

// =============================================================================
// Migration handlers — auth, invalid JSON, missing fields
// =============================================================================

func TestGrpE_MigrateCPanel_ResellerForbidden(t *testing.T) {
	s := grpEResellerServer()
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("POST", "/api/v1/migrate/cpanel", strings.NewReader("{}")))
	s.handleMigrateCPanel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_BulkDomainImport_ResellerForbidden(t *testing.T) {
	s := grpEResellerServer()
	rec := httptest.NewRecorder()
	req := withResellerContext(httptest.NewRequest("POST", "/api/v1/domains/bulk-import", strings.NewReader(`{"domains":[]}`)))
	s.handleBulkDomainImport(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_BulkDomainImport_InvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains/bulk-import", strings.NewReader("not json")))
	s.handleBulkDomainImport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_BulkDomainImport_AddsAndSkips(t *testing.T) {
	s := testServer() // already has example.com
	rec := httptest.NewRecorder()
	body := `{"domains":[{"host":"new1.com"},{"host":"example.com"},{"host":""}]}`
	req := withAdminContext(httptest.NewRequest("POST", "/api/v1/domains/bulk-import", strings.NewReader(body)))
	s.handleBulkDomainImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "new1.com") {
		t.Fatalf("body = %s, want new1.com added", rec.Body.String())
	}
}

func TestGrpE_CertUpload_ResellerForbidden(t *testing.T) {
	s := grpEResellerServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/certs/example.com/upload", strings.NewReader(`{}`))
	req.SetPathValue("host", "example.com")
	s.handleCertUpload(rec, withResellerContext(req))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

func TestGrpE_CertUpload_Validation(t *testing.T) {
	s := testServer()

	// invalid JSON
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/certs/example.com/upload", strings.NewReader("not json"))
	req.SetPathValue("host", "example.com")
	s.handleCertUpload(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}

	// missing cert/key
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/certs/example.com/upload", strings.NewReader(`{"cert":"x"}`))
	req.SetPathValue("host", "example.com")
	s.handleCertUpload(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}

	// invalid hostname (path traversal)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/certs/bad/upload", strings.NewReader(`{"cert":"c","key":"k"}`))
	req.SetPathValue("host", "../escape")
	s.handleCertUpload(rec, withAdminContext(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid hostname status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// Migration pure helpers
// =============================================================================

func TestGrpE_ResolveClonePaths(t *testing.T) {
	s, root := testServerWithRoot(t) // example.com → root

	// Source resolves via configured domain; target defaults under WebRoot.
	req := &migrate.CloneRequest{SourceDomain: "example.com", TargetDomain: "target.com"}
	if err := s.resolveClonePaths(req); err != nil {
		t.Fatalf("resolveClonePaths error: %v", err)
	}
	if req.SourceRoot != root {
		t.Fatalf("SourceRoot = %q, want %q", req.SourceRoot, root)
	}
	if !strings.HasSuffix(req.TargetRoot, filepath.Join("target.com", "public_html")) {
		t.Fatalf("TargetRoot = %q, want suffix target.com/public_html", req.TargetRoot)
	}

	// Explicit roots are preserved.
	req = &migrate.CloneRequest{
		SourceDomain: "example.com", TargetDomain: "t.com",
		SourceRoot: "/custom/src", TargetRoot: "/custom/dst",
	}
	if err := s.resolveClonePaths(req); err != nil {
		t.Fatalf("resolveClonePaths error: %v", err)
	}
	if req.SourceRoot != "/custom/src" || req.TargetRoot != "/custom/dst" {
		t.Fatalf("explicit roots overwritten: %#v", req)
	}
}

func TestGrpE_ResolveClonePaths_SourceNotFound(t *testing.T) {
	// Empty WebRoot + unknown domain → domainRoot returns "" → error branch.
	cfg := &config.Config{
		Global:  config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}},
		Domains: []config.Domain{},
	}
	s := grpEServerFromConfig(cfg)
	req := &migrate.CloneRequest{SourceDomain: "nope.com", TargetDomain: "t.com"}
	if err := s.resolveClonePaths(req); err == nil {
		t.Fatal("expected error for unknown source domain with empty WebRoot")
	}
}

func TestGrpE_ResolveClonePaths_DefaultWebRoot(t *testing.T) {
	// Server with no Global.WebRoot but a domain with an explicit root → target
	// defaults to /var/www base.
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{Host: "src.com", Type: "static", Root: "/srv/src.com", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	s := grpEServerFromConfig(cfg)
	req := &migrate.CloneRequest{SourceDomain: "src.com", TargetDomain: "dst.com"}
	if err := s.resolveClonePaths(req); err != nil {
		t.Fatalf("resolveClonePaths error: %v", err)
	}
	if !strings.HasPrefix(req.TargetRoot, "/var/www") {
		t.Fatalf("TargetRoot = %q, want /var/www prefix", req.TargetRoot)
	}
}

func TestGrpE_DetectWordPressDB(t *testing.T) {
	dir := t.TempDir()

	// No wp-config → no change.
	req := &migrate.CloneRequest{SourceRoot: dir}
	detectWordPressDB(req)
	if req.SourceDB != "" {
		t.Fatalf("SourceDB = %q, want empty when no wp-config", req.SourceDB)
	}

	// Already has SourceDB → early return, no parsing.
	req = &migrate.CloneRequest{SourceRoot: dir, SourceDB: "preset"}
	detectWordPressDB(req)
	if req.SourceDB != "preset" {
		t.Fatalf("SourceDB = %q, want preset preserved", req.SourceDB)
	}

	// Real wp-config.php → parse DB_NAME / DB_USER / DB_PASSWORD.
	wpCfg := "<?php\n" +
		"define('DB_NAME', 'mydb');\n" +
		"define('DB_USER', 'myuser');\n" +
		"define('DB_PASSWORD', 'mypass');\n"
	if err := os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte(wpCfg), 0600); err != nil {
		t.Fatal(err)
	}
	req = &migrate.CloneRequest{SourceRoot: dir}
	detectWordPressDB(req)
	if req.SourceDB != "mydb" || req.DBUser != "myuser" || req.DBPass != "mypass" {
		t.Fatalf("parsed DB creds = %#v, want mydb/myuser/mypass", req)
	}
}

// grpEServerFromConfig builds a server from a custom config (no auth manager).
func grpEServerFromConfig(cfg *config.Config) *Server {
	s := testServer()
	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()
	return s
}
