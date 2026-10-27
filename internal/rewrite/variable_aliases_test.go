package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

// F29702: %{SCRIPT_FILENAME} (Apache documents it as the same value as
// %{REQUEST_FILENAME}) and %{REMOTE_HOST} (the client address while
// HostnameLookups is off) were unknown variables and expanded to "". A
// "RewriteCond %{SCRIPT_FILENAME} !-f" front-controller guard then held for
// every request, sending existing files to the fallback.
func TestVariableAliasesExpand(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "real.css")
	if err := os.WriteFile(file, []byte("a{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := &Variables{RequestFilename: file, RemoteAddr: "203.0.113.9"}

	cases := []struct{ ref, want string }{
		{"%{SCRIPT_FILENAME}", file},
		{"%{script_filename}", file},
		{"%{REMOTE_HOST}", "203.0.113.9"},
		{"%{REQUEST_FILENAME}", file},     // control
		{"%{REMOTE_ADDR}", "203.0.113.9"}, // control
		{"%{NO_SUCH_VARIABLE}", ""},       // control: still unknown
	}
	for _, c := range cases {
		got := vars.Expand(c.ref)
		t.Logf("EXPECTED: %s = %q\nACTUAL: %q", c.ref, c.want, got)
		if got != c.want {
			t.Errorf("PROBLEM CONFIRMED: %s expanded to %q, want %q", c.ref, got, c.want)
		}
	}

	cond, err := ParseCondition("%{SCRIPT_FILENAME}", "-f", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := cond.Evaluate(vars); !ok {
		t.Errorf("PROBLEM CONFIRMED: -f on %%{SCRIPT_FILENAME} did not see %s", file)
	}
}
