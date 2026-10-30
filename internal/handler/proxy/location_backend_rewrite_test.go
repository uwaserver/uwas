package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// F40302: the proxy sends the backend's own address as Host, so a backend that
// builds an absolute redirect from the Host it was given (Apache mod_dir, an
// nginx trailing-slash redirect, most framework redirect helpers) answered with
// `Location: http://127.0.0.1:3000/dir/`. UWAS passed that through verbatim,
// sending the visitor to an address that is unreachable from outside and
// disclosing the internal topology. Like nginx's default proxy_redirect, a
// Location that names the backend itself is rewritten to the client-facing
// scheme and host.
func serveLocation(t *testing.T, https bool, loc func(host string) string) *httptest.ResponseRecorder {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", loc(r.Host))
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	t.Cleanup(up.Close)
	pool := NewUpstreamPool([]UpstreamConfig{{Address: up.URL, Weight: 1}})
	req := httptest.NewRequest("GET", "/dir", nil)
	req.Host = "app.example.test"
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.IsHTTPS = https
	New(newTestLogger()).Serve(ctx, newTestDomain(), pool, NewBalancer("round_robin"))
	return rec
}

func TestBackendRedirectLocationRewrittenToClientHost(t *testing.T) {
	// Controls: relative, external and other-port absolute Locations are the
	// backend's to decide and pass through untouched.
	for _, ctl := range []string{"/dir/", "https://other.example/x", "http://127.0.0.1:1/x"} {
		if got := serveLocation(t, false, func(string) string { return ctl }).Header().Get("Location"); got != ctl {
			t.Errorf("control: Location %q rewritten to %q", ctl, got)
		}
	}

	cases := []struct {
		name  string
		https bool
		loc   func(host string) string
		want  string
	}{
		{"path", false, func(h string) string { return "http://" + h + "/dir/" }, "http://app.example.test/dir/"},
		{"query and fragment", false, func(h string) string { return "http://" + h + "/a/b?x=1&y=%2F#frag" }, "http://app.example.test/a/b?x=1&y=%2F#frag"},
		{"no path", false, func(h string) string { return "http://" + h }, "http://app.example.test"},
		{"upper-case scheme and host", false, func(h string) string { return "HTTP://" + h + "/x" }, "http://app.example.test/x"},
		{"client over TLS", true, func(h string) string { return "http://" + h + "/dir/" }, "https://app.example.test/dir/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serveLocation(t, c.https, c.loc)
			got := rec.Header().Get("Location")
			t.Logf("EXPECTED: %s\nACTUAL: %s", c.want, got)
			if got != c.want {
				t.Errorf("PROBLEM CONFIRMED: Location = %q, want %q", got, c.want)
			}
			if rec.Code != http.StatusMovedPermanently {
				t.Errorf("status = %d, want 301", rec.Code)
			}
		})
	}
}
