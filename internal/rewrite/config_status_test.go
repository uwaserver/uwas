package rewrite

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A config rewrite "status" outside the redirect codes must not reach
// http.Redirect as-is (F26300): 200/404 are not redirects and 99 panics.
func TestConvertConfigRewritesStatusIsARedirectCode(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{301, 301}, {302, 302}, {303, 303}, {307, 307}, {308, 308},
		{200, 302}, {404, 302}, {99, 302}, {1000, 302},
	} {
		rules := ConvertConfigRewrites([]ConfigRewrite{{Match: "^/old$", To: "/new", Status: c.in}})
		res := NewEngine(rules).Process("/old", "", &Variables{})
		if !res.Redirect || res.StatusCode != c.want {
			t.Errorf("status %d: redirect=%v code=%d, want %d", c.in, res.Redirect, res.StatusCode, c.want)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/old", nil)
		http.Redirect(rec, req, res.URI, res.StatusCode) // must not panic
		if rec.Code != c.want {
			t.Errorf("status %d: wrote %d, want %d", c.in, rec.Code, c.want)
		}
	}
	// No status configured: plain rule, no redirect.
	rules := ConvertConfigRewrites([]ConfigRewrite{{Match: "^/old$", To: "/new"}})
	if res := NewEngine(rules).Process("/old", "", &Variables{}); res.Redirect || res.URI != "/new" {
		t.Errorf("no status: %+v", res)
	}
}
