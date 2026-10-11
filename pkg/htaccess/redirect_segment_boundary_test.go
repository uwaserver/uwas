package htaccess

import (
	"net/url"
	"testing"
)

// A plain Redirect matches whole path segments, as mod_alias does. A raw
// string-prefix match let "/old@evil.com" append "@evil.com" to a scheme+host
// target and redirect the visitor off-site (F2710).
func TestMatchRedirectSegmentBoundary(t *testing.T) {
	abs := RedirectRule{Status: 301, Pattern: "/old", Target: "https://new.example.com"}
	rel := RedirectRule{Status: 301, Pattern: "/old", Target: "/new"}
	slash := RedirectRule{Status: 301, Pattern: "/old/", Target: "https://new.example.com/n/"}
	root := RedirectRule{Status: 301, Pattern: "/", Target: "https://new.example.com/"}

	cases := []struct {
		name string
		rule RedirectRule
		path string
		want string // "" = no match
	}{
		{"exact", abs, "/old", "https://new.example.com"},
		{"subpath", abs, "/old/page", "https://new.example.com/page"},
		{"trailing slash", abs, "/old/", "https://new.example.com/"},
		{"userinfo trick", abs, "/old@evil.com", ""},
		{"port trick", abs, "/old:80@evil.com", ""},
		{"label trick", abs, "/old.evil.com", ""},
		{"longer segment", abs, "/older", ""},
		{"relative target exact", rel, "/old", "/new"},
		{"relative target sub", rel, "/old/x", "/new/x"},
		{"relative target longer segment", rel, "/oldx", ""},
		{"slash pattern sub", slash, "/old/x", "https://new.example.com/n/x"},
		{"slash pattern exact dir", slash, "/old/", "https://new.example.com/n/"},
		{"slash pattern other", slash, "/older/x", ""},
		{"root pattern", root, "/anything", "https://new.example.com/anything"},
	}
	for _, c := range cases {
		loc, _, ok := MatchRedirect(c.rule, c.path)
		if c.want == "" {
			if ok {
				t.Errorf("%s: %q matched with Location %q, want no match", c.name, c.path, loc)
			}
			continue
		}
		if !ok || loc != c.want {
			t.Errorf("%s: %q => %q ok=%v, want %q", c.name, c.path, loc, ok, c.want)
		}
		if u, err := url.Parse(loc); err == nil && u.Host != "" && u.Host != "new.example.com" {
			t.Errorf("%s: %q redirects to host %q", c.name, c.path, u.Host)
		}
	}
}
