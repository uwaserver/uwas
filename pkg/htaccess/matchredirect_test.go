package htaccess

import "testing"

// TestMatchRedirect pins the mod_alias semantics the server's applyHtaccess
// relies on: prefix match with suffix carry, regex backrefs, gone handling,
// and fail-closed behavior for broken patterns.
func TestMatchRedirect(t *testing.T) {
	cases := []struct {
		name     string
		rule     RedirectRule
		path     string
		wantLoc  string
		wantStat int
		wantOK   bool
	}{
		{
			name:     "exact prefix match carries no suffix",
			rule:     RedirectRule{Status: 301, Pattern: "/old.html", Target: "/new.html"},
			path:     "/old.html",
			wantLoc:  "/new.html",
			wantStat: 301,
			wantOK:   true,
		},
		{
			name:     "prefix match carries the remainder (Apache mod_alias)",
			rule:     RedirectRule{Status: 301, Pattern: "/old", Target: "/new"},
			path:     "/old/docs/a",
			wantLoc:  "/new/docs/a",
			wantStat: 301,
			wantOK:   true,
		},
		{
			name:     "suffix is carried onto absolute targets too",
			rule:     RedirectRule{Pattern: "/blog", Target: "https://blog.example.com/posts"},
			path:     "/blog/2024/x",
			wantLoc:  "https://blog.example.com/posts/2024/x",
			wantStat: 302,
			wantOK:   true,
		},
		{
			name:     "regex match expands $1 backref",
			rule:     RedirectRule{Status: 302, IsRegex: true, Pattern: `^/legacy/(.*)$`, Target: "/modern/$1"},
			path:     "/legacy/docs/a",
			wantLoc:  "/modern/docs/a",
			wantStat: 302,
			wantOK:   true,
		},
		{
			name:     "out-of-range backref expands to empty",
			rule:     RedirectRule{IsRegex: true, Pattern: `^/x/(\w+)$`, Target: "/y/$2/end"},
			path:     "/x/abc",
			wantLoc:  "/y//end",
			wantStat: 302,
			wantOK:   true,
		},
		{
			name:     "empty target means gone",
			rule:     RedirectRule{Status: 302, Pattern: "/dead"}, // gone-form misparse neutralized by the applier
			path:     "/dead/page",
			wantLoc:  "",
			wantStat: 302,
			wantOK:   true,
		},
		{
			name:   "non-matching path is not ok",
			rule:   RedirectRule{Status: 301, Pattern: "/old", Target: "/new"},
			path:   "/other",
			wantOK: false,
		},
		{
			name:   "uncompilable regex fails closed",
			rule:   RedirectRule{IsRegex: true, Pattern: `^/([unclosed`, Target: "/new"},
			path:   "/anything",
			wantOK: false,
		},
		{
			name:   "empty pattern never matches",
			rule:   RedirectRule{Pattern: "", Target: "/new"},
			path:   "/anything",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, stat, ok := MatchRedirect(tc.rule, tc.path)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if loc != tc.wantLoc {
				t.Errorf("location = %q, want %q", loc, tc.wantLoc)
			}
			if stat != tc.wantStat {
				t.Errorf("status = %d, want %d", stat, tc.wantStat)
			}
		})
	}
}
