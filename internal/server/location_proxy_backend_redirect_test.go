package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// F40302 on the location proxy: a backend that builds its redirect from the
// Host it received answered with its own address, which the visitor cannot
// reach. A Location naming the backend is rewritten to the client-facing
// host, as on the domain proxy. A proxy_pass with a base path or
// strip_prefix remaps paths too, so it is left alone.
func locationRedirectGet(t *testing.T, proxyPassSuffix, extra string, loc func(host string) string) string {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", loc(r.Host))
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(up.Close)
	s := nonCanonServer(t, "", fmt.Sprintf(`    proxy: {allow_private_upstreams: true}
    locations:
      - match: /api/
        proxy_pass: %s%s
%s`, up.URL, proxyPassSuffix, extra))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Host = "a.test"
	req.RemoteAddr = "192.0.2.5:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	return rec.Header().Get("Location")
}

func TestLocationProxyRewritesBackendRedirect(t *testing.T) {
	// Controls: relative and external Locations pass through untouched.
	for _, ctl := range []string{"/api/new", "https://other.example/x"} {
		if got := locationRedirectGet(t, "", "", func(string) string { return ctl }); got != ctl {
			t.Errorf("control: Location %q became %q", ctl, got)
		}
	}

	got := locationRedirectGet(t, "", "", func(h string) string { return "http://" + h + "/api/dir/?q=1" })
	t.Logf("EXPECTED: http://a.test/api/dir/?q=1\nACTUAL: %s", got)
	if got != "http://a.test/api/dir/?q=1" {
		t.Errorf("PROBLEM CONFIRMED: Location = %q, want the client-facing host", got)
	}

	// A base path remaps paths; the Location is not rewritten (and not
	// touched at all).
	base := locationRedirectGet(t, "/base", "", func(h string) string { return "http://" + h + "/base/dir/" })
	if base == "http://a.test/base/dir/" || base == "http://a.test/dir/" {
		t.Errorf("Location rewritten although proxy_pass has a base path: %q", base)
	}
}
