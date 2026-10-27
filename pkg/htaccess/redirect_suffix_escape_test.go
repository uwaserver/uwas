package htaccess

import "testing"

// F30601: Redirect carries the unmatched suffix of the request path onto the
// target. The suffix comes from the decoded path, so "/old/a%3Fb" produced
// "/new/a?b" (the path byte became a query delimiter) and "%23" a fragment.
// Apache escapes the suffix (ap_os_escape_path), keeping "%3F" / "%23" intact.
func TestMatchRedirectEscapesCarriedSuffix(t *testing.T) {
	rule := RedirectRule{Pattern: "/old", Target: "/new", Status: 301}
	absolute := RedirectRule{Pattern: "/old", Target: "https://new.example", Status: 301}
	cases := []struct {
		name string
		rule RedirectRule
		path string
		want string
	}{
		{"question mark in path", rule, "/old/a?b", "/new/a%3Fb"},
		{"hash in path", rule, "/old/a#b", "/new/a%23b"},
		{"space in path", rule, "/old/a b", "/new/a%20b"},
		{"percent in path", rule, "/old/100%", "/new/100%25"},
		{"absolute target", absolute, "/old/a?b", "https://new.example/a%3Fb"},
		{"control: plain path", rule, "/old/a/b.html", "/new/a/b.html"},
		{"control: exact", rule, "/old", "/new"},
		{"control: unreserved and slash", rule, "/old/a-b_c.~/d", "/new/a-b_c.~/d"},
	}
	for _, c := range cases {
		got, _, ok := MatchRedirect(c.rule, c.path)
		t.Logf("%s\nEXPECTED: %s\nACTUAL: %s", c.name, c.want, got)
		if !ok || got != c.want {
			t.Errorf("PROBLEM CONFIRMED: %s: Location = %q, want %q", c.name, got, c.want)
		}
	}
}
