package admin

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/admin/dashboard"
)

func dashboardGet(s *Server, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// F58600: a missing build asset (stale shell after an upgrade) answered 200
// text/html, which the browser cannot execute as a module/stylesheet.
func TestDashboardMissingAssetIs404(t *testing.T) {
	s := testServer()
	for _, p := range []string{
		"/_uwas/dashboard/assets/Old-deadbeef.js",
		"/_uwas/dashboard/assets/missing.css",
		"/_uwas/dashboard/assets/",
		"/_uwas/dashboard/assets",
	} {
		if rec := dashboardGet(s, "GET", p); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 (ct=%q)", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// Control: client-side routes (even with a dotted last segment) keep the shell.
	for _, p := range []string{"/_uwas/dashboard/", "/_uwas/dashboard/domains/example.com", "/_uwas/dashboard/login"} {
		rec := dashboardGet(s, "GET", p)
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s = %d ct=%q, want 200 text/html", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// F58601: the shell names content-hashed assets so it must revalidate; the
// hashed assets themselves are immutable.
func TestDashboardCacheHeaders(t *testing.T) {
	s := testServer()
	if cc := dashboardGet(s, "GET", "/_uwas/dashboard/").Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("shell Cache-Control = %q, want no-cache", cc)
	}
	if cc := dashboardGet(s, "GET", "/_uwas/dashboard/some/deep/route").Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("deep route Cache-Control = %q, want no-cache", cc)
	}
	distFS, err := fs.Sub(dashboard.Assets, "dist")
	if err != nil {
		t.Skip("no embedded dist")
	}
	entries, err := fs.ReadDir(distFS, "assets")
	if err != nil || len(entries) == 0 {
		t.Skip("no embedded assets")
	}
	rec := dashboardGet(s, "GET", "/_uwas/dashboard/assets/"+entries[0].Name())
	if rec.Code != 200 {
		t.Fatalf("real asset = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", cc)
	}
	// Control: non-hashed root files are not marked immutable.
	if cc := dashboardGet(s, "GET", "/_uwas/dashboard/favicon.svg").Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("favicon Cache-Control = %q, must not be immutable", cc)
	}
}

// F58602: the mount answered every method with the shell.
func TestDashboardRejectsWriteMethods(t *testing.T) {
	s := testServer()
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if rec := dashboardGet(s, m, "/_uwas/dashboard/anything"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", m, rec.Code)
		}
	}
	for _, m := range []string{"GET", "HEAD"} {
		if rec := dashboardGet(s, m, "/_uwas/dashboard/"); rec.Code != 200 {
			t.Errorf("%s = %d, want 200", m, rec.Code)
		}
	}
}
