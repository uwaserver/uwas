package pathmatch

// An empty `match` is UNSET, not a wildcard. strings.HasPrefix(path, "") is
// true for every path, so without an explicit guard a location block with no
// match became a domain-wide catch-all — it applied its headers and
// Cache-Control to every request and, because every caller breaks on first
// match, suppressed all location blocks after it.
//
// This mirrors how the rest of the codebase already reads an absent match:
// config validation guards `if loc.Match != ""` (validate.go), and the rewrite
// engine drops a rule whose pattern will not parse instead of applying it
// everywhere.

import "testing"

func TestLocationEmptyPatternIsNotCatchAll(t *testing.T) {
	for _, path := range []string{"/", "/index.html", "/api/v1/users", "/admin/secret", "/wp-login.php"} {
		if Location(path, "") {
			t.Errorf("Location(%q, %q) = true; an unset match must match nothing", path, "")
		}
	}
}

func TestLocationScopedPatternsStillMatch(t *testing.T) {
	// Prefix form: the control that an explicit prefix keeps working.
	for _, tc := range []struct {
		path, pattern string
		want          bool
	}{
		{"/api/v1", "/api/", true},
		{"/api/v1", "/assets/", false},
		{"/index.php", "/", true},
		// Regex form via the "~" prefix.
		{"/index.php", `~\.php$`, true},
		{"/index.html", `~\.php$`, false},
		// An uncompilable regex still fails closed rather than matching.
		{"/anything", "~[", false},
	} {
		if got := Location(tc.path, tc.pattern); got != tc.want {
			t.Errorf("Location(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

func TestLocationEmptyPatternDiffersFromSlash(t *testing.T) {
	// "" and "/" both look like "everything", but only "/" is a real
	// catch-all. Keep them distinguishable so the guard cannot be widened.
	if !Location("/anything", "/") {
		t.Error(`Location("/anything", "/") = false; an explicit "/" prefix must still match`)
	}
	if Location("/anything", "") {
		t.Error(`Location("/anything", "") = true; an unset match must not behave like "/"`)
	}
}
