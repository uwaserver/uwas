package proxy

import (
	"net/http"
	"net/url"
	"testing"
)

// Unit table for RewriteBackendLocation (F40302).
func TestRewriteBackendLocation(t *testing.T) {
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	cases := []struct {
		name, backend, loc, scheme, host, want string
	}{
		{"same authority", "http://127.0.0.1:3000", "http://127.0.0.1:3000/x", "https", "a.test", "https://a.test/x"},
		{"default port on one side only (http)", "http://app.internal", "http://app.internal:80/x", "http", "a.test", "http://a.test/x"},
		{"default port on one side only (https)", "https://app.internal:443", "https://app.internal/x", "https", "a.test", "https://a.test/x"},
		{"host is case-insensitive", "http://App.Internal:3000", "http://app.internal:3000/x", "http", "a.test", "http://a.test/x"},
		{"userinfo in Location is dropped", "http://127.0.0.1:3000", "http://u:p@127.0.0.1:3000/x", "http", "a.test", "http://a.test/x"},
		{"different port untouched", "http://127.0.0.1:3000", "http://127.0.0.1:3001/x", "http", "a.test", "http://127.0.0.1:3001/x"},
		{"different host untouched", "http://127.0.0.1:3000", "http://example.org:3000/x", "http", "a.test", "http://example.org:3000/x"},
		{"relative untouched", "http://127.0.0.1:3000", "/x", "http", "a.test", "/x"},
		{"scheme-relative untouched", "http://127.0.0.1:3000", "//127.0.0.1:3000/x", "http", "a.test", "//127.0.0.1:3000/x"},
		{"non-http scheme untouched", "http://127.0.0.1:3000", "ftp://127.0.0.1:3000/x", "http", "a.test", "ftp://127.0.0.1:3000/x"},
		{"unparsable untouched", "http://127.0.0.1:3000", "http://127.0.0.1:3000/%zz", "http", "a.test", "http://127.0.0.1:3000/%zz"},
		{"empty untouched", "http://127.0.0.1:3000", "", "http", "a.test", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.loc != "" {
				h.Set("Location", c.loc)
			}
			RewriteBackendLocation(h, mustURL(c.backend), c.scheme, c.host)
			if got := h.Get("Location"); got != c.want {
				t.Errorf("Location = %q, want %q", got, c.want)
			}
		})
	}
}
