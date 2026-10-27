package rewrite

import "testing"

// F29700: a substitution that is an absolute URL ("https://new.example/") used
// without an [R] flag was kept as the request path, so
// "RewriteRule ^old$ https://new.example/ [L]" (the usual Apache spelling,
// which implies an external redirect) answered 404 for "https://new.example/".
// Only a literal scheme in the rule's own target counts: a path captured from
// the request ("$1" of "/http://x") must never turn into a redirect.
func TestAbsoluteTargetWithoutRFlagRedirects(t *testing.T) {
	cases := []struct {
		name, pattern, target, flags, uri string
		wantRedirect                      bool
		wantStatus                        int
		wantURI                           string
	}{
		{"absolute https target", "^/old$", "https://new.example/dest", "L", "/old", true, 302, "https://new.example/dest"},
		{"absolute http target, upper case", "^/old$", "HTTP://new.example/dest", "L", "/old", true, 302, "HTTP://new.example/dest"},
		{"explicit status still wins", "^/old$", "https://new.example/dest", "R=301,L", "/old", true, 301, "https://new.example/dest"},
		{"relative target is internal (control)", "^/old$", "/dest", "L", "/old", false, 0, "/dest"},
		{"captured scheme is not a redirect (control)", "^/(.*)$", "$1", "L", "/http://evil.example/", false, 0, "http://evil.example/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, err := ParseRule(c.pattern, c.target, c.flags)
			if err != nil {
				t.Fatal(err)
			}
			r := NewEngine([]*Rule{rule}).Process(c.uri, "", &Variables{RequestURI: c.uri})
			t.Logf("EXPECTED: redirect=%v status=%d uri=%q\nACTUAL: redirect=%v status=%d uri=%q",
				c.wantRedirect, c.wantStatus, c.wantURI, r.Redirect, r.StatusCode, r.URI)
			if r.Redirect != c.wantRedirect || r.StatusCode != c.wantStatus || r.URI != c.wantURI {
				t.Fatal("PROBLEM CONFIRMED")
			}
		})
	}
}
