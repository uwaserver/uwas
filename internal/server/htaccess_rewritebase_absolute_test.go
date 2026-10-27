package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// F30600: RewriteBase is prepended only to relative substitutions. An absolute
// URL target (scheme://) is already complete; prefixing it produced
// "/blog/https://new.example/" and the redirect went nowhere.
func TestHtaccessRewriteBaseLeavesAbsoluteURLAlone(t *testing.T) {
	s := htRewriteServer(t, map[string]string{
		"abs.test": "RewriteEngine On\nRewriteBase /blog/\nRewriteRule ^old$ https://new.example/landing [R=301,L]\n",
		"rel.test": "RewriteEngine On\nRewriteBase /blog/\nRewriteRule ^old$ page.txt [R=302,L]\n",
	})
	for _, c := range []struct{ host, want string }{
		{"abs.test", "https://new.example/landing"},
		{"rel.test", "/blog/page.txt"}, // control: relative target still gets the base
	} {
		req := httptest.NewRequest(http.MethodGet, "/old", nil)
		req.Host = c.host
		req.RemoteAddr = "198.51.100.7:1"
		req.Header.Set("User-Agent", "uwas-test")
		rr := httptest.NewRecorder()
		s.handler.ServeHTTP(rr, req)
		got := rr.Header().Get("Location")
		t.Logf("EXPECTED: %s\nACTUAL: %s (status %d)", c.want, got, rr.Code)
		if got != c.want {
			t.Errorf("PROBLEM CONFIRMED: %s Location = %q, want %q", c.host, got, c.want)
		}
	}
}
