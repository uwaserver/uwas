package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// F2501: a WAF bypass prefix is matched against the canonical path. Matching
// the path as sent let "/wp-admin/../shop?q=UNION SELECT" skip the WAF while
// the request was served as "/shop".
func TestDomainWAFBypassPrefixUsesCanonicalPath(t *testing.T) {
	g := DomainWAFGuard(logger.New("error", "text"), []string{"/wp-admin/"}, nil, nil)
	run := func(path string) bool {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = path
		req.URL.RawQuery = "q=1%20UNION%20SELECT%201,2"
		req.RemoteAddr = "192.0.2.5:1"
		return g(httptest.NewRecorder(), req)
	}
	if run("/shop") {
		t.Fatal("attack on a non-exempt path was allowed")
	}
	for _, p := range []string{"/wp-admin/admin-ajax.php", "/wp-admin/", "//wp-admin/x", "/./wp-admin/x"} {
		if !run(p) {
			t.Errorf("exempt path %q must still bypass", p)
		}
	}
	for _, p := range []string{"/wp-admin/../shop", "/wp-admin/../", "/wp-admin//../shop", "/wp-admin/x/../../shop"} {
		if run(p) {
			t.Errorf("%q starts with the exempt prefix but is served elsewhere; the WAF must still run", p)
		}
	}
}
