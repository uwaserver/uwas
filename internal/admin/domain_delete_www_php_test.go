package admin

// F64500: PHP assignment of a deleted domain is released under the
// stored host for www records, deleted by either spelling; plain records and
// unrelated domains behave as before; a repeated delete is a 404 no-op.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

func phpAssignedTo(m *phpmanager.Manager, host string) bool {
	for _, d := range m.GetDomainInstances() {
		if d.Domain == host {
			return true
		}
	}
	return false
}

func TestDeleteWWWRecordReleasesPHPAssignment(t *testing.T) {
	webRoot := t.TempDir()
	mk := func(h string) config.Domain {
		return config.Domain{Host: h, Root: webRoot + "/" + h + "/public_html", Type: "php", SSL: config.SSLConfig{Mode: "off"}}
	}
	cfg := &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot},
		Domains: []config.Domain{mk("www.a.test"), mk("www.b.test"), mk("plain.test"), mk("keep.test")},
	}
	s := testServerFromConfig(t, cfg)
	mgr := phpmanager.New(testLogger(t))
	s.SetPHPManager(mgr)
	for i, h := range []string{"www.a.test", "www.b.test", "plain.test", "keep.test"} {
		mgr.RegisterExistingDomain(h, "8.3", "127.0.0.1:"+string(rune('0'+i))+"9201", webRoot+"/"+h+"/public_html", map[string]string{"memory_limit": "1G"})
	}
	del := func(h string) int {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/"+h+"?confirm=true", nil))
		return rec.Code
	}
	cases := []struct{ path, stored string }{
		{"www.a.test", "www.a.test"}, // www record by its own spelling
		{"b.test", "www.b.test"},     // www record by the stripped key
		{"plain.test", "plain.test"}, // control
	}
	for _, c := range cases {
		if code := del(c.path); code != http.StatusOK {
			t.Fatalf("delete %s: status %d", c.path, code)
		}
		if phpAssignedTo(mgr, c.stored) {
			t.Fatalf("FAIL: %s (deleted via %s) still assigned", c.stored, c.path)
		}
		if got := mgr.GetDomainConfig(c.stored); len(got) != 0 {
			t.Fatalf("FAIL: %s overrides survived: %v", c.stored, got)
		}
	}
	if !phpAssignedTo(mgr, "keep.test") {
		t.Fatal("FAIL: unrelated keep.test lost its assignment")
	}
	// Repeated delete: not found, no panic, keep.test untouched.
	if code := del("www.a.test"); code != http.StatusNotFound {
		t.Fatalf("FAIL: repeated delete status %d", code)
	}
	if !phpAssignedTo(mgr, "keep.test") {
		t.Fatal("FAIL: repeated delete touched keep.test")
	}
}
