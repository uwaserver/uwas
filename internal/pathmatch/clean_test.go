package pathmatch

import "testing"

// F2500: a location is matched against the canonical path, because the static
// handler opens files through filepath.Clean. Matching the path as sent let
// "//private/x" skip a location's basic_auth and still be served.
func TestCleanAndLocationUseCanonicalPath(t *testing.T) {
	clean := map[string]string{
		"/":                   "/",
		"":                    "",
		"*":                   "*",
		"/private/x":          "/private/x",
		"/private/":           "/private/",
		"//private/x":         "/private/x",
		"/./private/x":        "/private/x",
		"/a/../private/x":     "/private/x",
		"/private//":          "/private/",
		"/private/./":         "/private/",
		"/../private/x":       "/private/x",
		"/a/b/../../private/": "/private/",
	}
	for in, want := range clean {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}

	for _, p := range []string{"/private/x", "//private/x", "/./private/x", "/a/../private/x", "/private/"} {
		if !Location(p, "/private/") {
			t.Errorf("Location(%q, /private/) = false, want true", p)
		}
	}
	for _, p := range []string{"/privateer/x", "/other/private/x", "/private"} {
		if Location(p, "/private/") {
			t.Errorf("Location(%q, /private/) = true, want false", p)
		}
	}
	if !Location("/a/../x.php", `~\.php$`) || Location("/a/../x.txt", `~\.php$`) {
		t.Error("regex location must be matched on the canonical path")
	}
	if Location("/anything", "") {
		t.Error("empty pattern must stay unset")
	}
}
