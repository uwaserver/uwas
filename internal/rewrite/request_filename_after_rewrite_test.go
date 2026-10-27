package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

// F29701: after a rule rewrote the URI, %{REQUEST_URI} was updated but
// %{REQUEST_FILENAME} kept naming the file of the ORIGINAL request, so a later
// "RewriteCond %{REQUEST_FILENAME} !-f" tested a path that is no longer being
// served. "/assets/x.css -> /static/x.css" followed by the usual front-controller
// fallback sent a request for an existing file to /index.php.
func TestRequestFilenameFollowsRewrittenURI(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "static", "x.css"), []byte("a{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	move, err := ParseRule("^/assets/(.*)$", "/static/$1", "")
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := ParseRule("^", "/index.php", "L")
	if err != nil {
		t.Fatal(err)
	}
	cond, err := ParseCondition("%{REQUEST_FILENAME}", "!-f", "")
	if err != nil {
		t.Fatal(err)
	}
	fallback.Conditions = append(fallback.Conditions, *cond)

	cases := []struct{ name, uri, want string }{
		{"rewritten to an existing file", "/assets/x.css", "/static/x.css"},
		{"rewritten to a missing file", "/assets/none.css", "/index.php"},
		{"not rewritten, existing file (control)", "/static/x.css", "/static/x.css"},
		{"not rewritten, missing file (control)", "/static/none.css", "/index.php"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars := &Variables{
				RequestURI:      c.uri,
				DocumentRoot:    root,
				RequestFilename: filepath.Join(root, filepath.Clean(c.uri)),
			}
			got := NewEngine([]*Rule{move, fallback}).Process(c.uri, "", vars).URI
			t.Logf("EXPECTED: %s\nACTUAL: %s", c.want, got)
			if got != c.want {
				t.Fatal("PROBLEM CONFIRMED")
			}
		})
	}
}
