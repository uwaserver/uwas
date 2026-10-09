package wordpress

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fileEditDefineRe = regexp.MustCompile(`(?i)define\s*\(\s*['"]DISALLOW_FILE_EDIT['"]\s*,\s*(true|false)\s*\)`)

func hardenFileEdit(t *testing.T, cfg string, value bool) (string, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "wp-config.php")
	if err := os.WriteFile(p, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Harden(dir, HardenOptions{DisableFileEdit: &value})
	out, rerr := os.ReadFile(p)
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(out), err
}

func fileEditDefines(s string) []string {
	var r []string
	for _, m := range fileEditDefineRe.FindAllStringSubmatch(s, -1) {
		r = append(r, m[1])
	}
	return r
}

// The older require_once(ABSPATH . ...) form must anchor the define; it used
// to strip the existing define and report success without re-adding it.
func TestHarden_LegacyRequireKeepsDefine(t *testing.T) {
	out, err := hardenFileEdit(t, "<?php\ndefine('DISALLOW_FILE_EDIT', true);\nrequire_once(ABSPATH . 'wp-settings.php');\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileEditDefines(out); strings.Join(got, ",") != "true" {
		t.Fatalf("defines = %v, want [true]; config:\n%s", got, out)
	}
}

func TestHarden_NoRequireFailsWithoutStripping(t *testing.T) {
	cfg := "<?php\ndefine('DISALLOW_FILE_EDIT', true);\n"
	out, err := hardenFileEdit(t, cfg, false)
	if err == nil {
		t.Fatal("expected an error when wp-config.php has no require_once ABSPATH")
	}
	if out != cfg {
		t.Fatalf("wp-config.php modified on failure:\n%s", out)
	}
}

// A double-quoted / uppercase define must be replaced, not shadowed by a later
// define PHP would ignore (the first define of a constant wins).
func TestHarden_DoubleQuotedDefineReplaced(t *testing.T) {
	out, err := hardenFileEdit(t, "<?php\ndefine( \"DISALLOW_FILE_EDIT\", FALSE );\nrequire_once ABSPATH . 'wp-settings.php';\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileEditDefines(out); strings.Join(got, ",") != "true" {
		t.Fatalf("defines = %v, want [true]; config:\n%s", got, out)
	}
}
