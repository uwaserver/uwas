package fastcgi

import (
	"strings"
	"testing"
)

// F42600: frameworkProjectRoot widens open_basedir to the parent of a
// public/web/html/htdocs document root so a Laravel or Symfony app can reach
// its project files. Its own comment promises that flat multi-tenant roots
// under /var/www stay isolated, but a site whose root is /var/www/html (the
// Apache default, and what an Apache import produces) got "/var/www/" added,
// which is every sibling tenant's web root.
func TestOpenBasedirDoesNotWidenToSharedWebBase(t *testing.T) {
	for _, root := range []string{
		"/var/www/html", "/var/www/htdocs", "/var/www/public", "/var/www/web",
		"/srv/www/html", "/srv/html", "/home/public", "/usr/share/nginx/html",
	} {
		ob := adminDirectives(t, uploadEnv(t, root, 0))["open_basedir"]
		t.Logf("EXPECTED: no entry covering the shared base of %s\nACTUAL: %s", root, ob)
		for _, e := range strings.Split(ob, ":") {
			switch e {
			case "/var/www/", "/srv/www/", "/srv/", "/home/", "/usr/share/nginx/":
				t.Errorf("PROBLEM CONFIRMED: root %q puts the shared base %q in open_basedir", root, e)
			}
		}
	}
}

// Control: a framework project root that is not a shared web base is still
// admitted, so Laravel/Symfony layouts keep working.
func TestOpenBasedirStillIncludesFrameworkProjectRoot(t *testing.T) {
	for _, tc := range []struct{ root, project string }{
		{"/var/www/app.example.com/public", "/var/www/app.example.com/"},
		{"/srv/app/public", "/srv/app/"},
		{"/var/www/site/htdocs", "/var/www/site/"},
	} {
		ob := adminDirectives(t, uploadEnv(t, tc.root, 0))["open_basedir"]
		found := false
		for _, e := range strings.Split(ob, ":") {
			if e == tc.project {
				found = true
			}
		}
		if !found {
			t.Errorf("root %q: open_basedir %q lacks project root %q", tc.root, ob, tc.project)
		}
	}
}
