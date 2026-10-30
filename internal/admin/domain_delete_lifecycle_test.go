package admin

// F51100: deleting a domain without ?cleanup=true left its PHP assignment
// behind. The php-cgi worker kept running (and listening) for a hostname that
// no longer exists, the PHP page kept listing it, and a hostname given to the
// next tenant inherited the previous owner's per-domain php.ini overrides
// (memory_limit, upload_max_filesize, ...) and worker address. Stopping and
// unassigning the worker is not destructive, so it no longer waits for the
// opt-in cleanup flag; the data-affecting steps (cron jobs, SFTP user) still do.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

func phpHasDomain(m *phpmanager.Manager, host string) bool {
	for _, d := range m.GetDomainInstances() {
		if d.Domain == host {
			return true
		}
	}
	return false
}

func TestDeleteDomainWithoutCleanupReleasesPHPAssignment(t *testing.T) {
	webRoot := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{WebRoot: webRoot},
		Domains: []config.Domain{
			{Host: "gone.test", Root: webRoot + "/gone.test/public_html", Type: "php", SSL: config.SSLConfig{Mode: "off"}},
			{Host: "keep.test", Root: webRoot + "/keep.test/public_html", Type: "php", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	s := testServerFromConfig(t, cfg)
	mgr := phpmanager.New(testLogger(t))
	s.SetPHPManager(mgr)
	mgr.RegisterExistingDomain("gone.test", "8.3", "127.0.0.1:19001", webRoot+"/gone.test/public_html",
		map[string]string{"memory_limit": "1G"})
	mgr.RegisterExistingDomain("keep.test", "8.3", "127.0.0.1:19002", webRoot+"/keep.test/public_html", nil)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/gone.test?confirm=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}

	t.Logf("EXPECTED: gone.test unassigned from PHP, keep.test untouched\nACTUAL: gone.test assigned=%v keep.test assigned=%v",
		phpHasDomain(mgr, "gone.test"), phpHasDomain(mgr, "keep.test"))
	if phpHasDomain(mgr, "gone.test") {
		t.Fatal("PROBLEM CONFIRMED: deleted domain still has a PHP assignment")
	}
	if !phpHasDomain(mgr, "keep.test") {
		t.Fatal("control: an unrelated domain must keep its PHP assignment")
	}

	// The hostname handed to the next tenant starts clean.
	body := `{"host":"gone.test","type":"static","root":"` + webRoot + `/gone.test/public_html","ssl":{"mode":"off"}}`
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-add status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := mgr.GetDomainConfig("gone.test"); len(got) != 0 {
		t.Fatalf("PROBLEM CONFIRMED: re-added hostname inherited the previous owner's php.ini overrides: %v", got)
	}
}

func TestDeleteDomainKeepsDataAffectingStepsOptIn(t *testing.T) {
	// The CLI and API default must not remove cron jobs or the SFTP user:
	// without cleanup=true the document root, cron jobs and site user stay.
	webRoot := t.TempDir()
	cfg := &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot},
		Domains: []config.Domain{{Host: "gone.test", Root: webRoot + "/gone.test/public_html", Type: "static", SSL: config.SSLConfig{Mode: "off"}}},
	}
	s := testServerFromConfig(t, cfg)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/gone.test", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "confirm") {
		t.Fatalf("delete without confirm = %d %s, want 400 asking for confirmation", rec.Code, rec.Body.String())
	}
}
