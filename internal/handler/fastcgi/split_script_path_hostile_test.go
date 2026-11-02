package fastcgi

import "testing"

// F61302: pins how hostile request paths split into SCRIPT_NAME and PATH_INFO
// (no PHP needed). The round-196 survey found no defect; the table keeps it
// from drifting: encoded slashes and dot segments never move PATH_INFO outside
// the request's own path, a NUL stays encoded, and a name that only looks like
// a PHP script (".phpx", ".php.jpg", ";.jpg") is not split at.
func TestSplitScriptPathHostilePaths(t *testing.T) {
	const root = "/var/www"
	cases := []struct {
		uri, resolved, wantScript, wantInfo string
	}{
		{"/index.php/x", "/var/www/index.php", "/index.php", "/x"},
		{"//index.php//x", "/var/www/index.php", "/index.php", "/x"},
		{"/index.php%2fx", "/var/www/index.php", "/index.php", "/x"},
		{"/index.php/%2e%2e/admin.php", "/var/www/index.php", "/index.php", "/admin.php"},
		{"/index.php/..%2f..%2fetc/passwd", "/var/www/index.php", "/index.php", "/etc/passwd"},
		{"/index.php/a%00b", "/var/www/index.php", "/index.php", "/a%00b"},
		{"/index.php/", "/var/www/index.php", "/index.php", "/"},
		{"/INDEX.PHP/x", "/var/www/INDEX.PHP", "/INDEX.PHP", "/x"},
		{"/blog/?p=1", "/var/www/index.php", "/index.php", "/blog/"},
		{"/index.php?/etc/passwd", "/var/www/index.php", "/index.php", ""},
		{"/sub/index.php/..", "/var/www/sub/index.php", "/sub/index.php", ""},
		// Not PHP scripts: handled by the index fallback, whole path as PATH_INFO.
		{"/index.phpx/y", "", "/index.php", "/index.phpx/y"},
		{"/foo.php.jpg", "", "/index.php", "/foo.php.jpg"},
		{"/a.php;.jpg", "", "/index.php", "/a.php;.jpg"},
	}
	for _, c := range cases {
		script, info := SplitScriptPath(c.uri, c.resolved, root, []string{"index.php"})
		if script != c.wantScript || info != c.wantInfo {
			t.Errorf("%q (%q): got (%q, %q), want (%q, %q)", c.uri, c.resolved, script, info, c.wantScript, c.wantInfo)
		}
	}
}
