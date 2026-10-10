package htaccess

import (
	"strings"
	"testing"
)

func parseConvert(t *testing.T, src string) *RuleSet {
	t.Helper()
	ds, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return Convert(ds)
}

// A <FilesMatch> regex RE2 cannot compile (PCRE lookahead, or a typo Apache
// would reject) must not silently drop the block's deny (F890).
func TestFilesMatchUncompilableRegexFailsClosed(t *testing.T) {
	cases := []struct{ src, file string }{
		{"<FilesMatch \"^(?!index\\.php$).*\\.php$\">\nRequire all denied\n</FilesMatch>\n", "shell.php"},
		{"<FilesMatch \"\\.(sql|bak$\">\nDeny from all\n</FilesMatch>\n", "dump.sql"},
		{"<Files ~ \"^(?=x)\">\nRequire all denied\n</Files>\n", "a.txt"},
	}
	for _, c := range cases {
		rs := parseConvert(t, c.src)
		if !FilesMatchDeniesFor(rs, c.file, nil) || !FilesMatchDenies(rs, c.file) {
			t.Errorf("%q: %s not denied", c.src, c.file)
		}
	}
	rs := parseConvert(t, "<FilesMatch \"\\.sql$\">\nRequire all denied\n</FilesMatch>\n")
	if FilesMatchDeniesFor(rs, "index.php", nil) {
		t.Error("compilable regex now over-matches")
	}
}

// Text after a section's closing '>' (e.g. a trailing comment) is ignored, as
// in Apache, instead of corrupting the pattern or module name (F891).
func TestSectionTrailingTextIgnored(t *testing.T) {
	rs := parseConvert(t, "<FilesMatch \"\\.sql$\"> # protect dumps\nRequire all denied\n</FilesMatch>\n")
	if !FilesMatchDeniesFor(rs, "dump.sql", nil) {
		t.Error("FilesMatch with trailing comment: dump.sql not denied")
	}
	if FilesMatchDeniesFor(rs, "a.txt", nil) {
		t.Error("FilesMatch with trailing comment over-matches")
	}
	rs = parseConvert(t, "<IfModule mod_authz_core.c> # 2.4\nRequire all denied\n</IfModule>\n")
	if rs.AccessFor(nil) != AccessDenied {
		t.Error("IfModule with trailing comment: top-level deny lost")
	}
	rs = parseConvert(t, "<Files \"a>b.txt\">\nRequire all denied\n</Files>\n")
	if !FilesMatchDeniesFor(rs, "a>b.txt", nil) {
		t.Error("'>' inside a quoted name was cut")
	}
}
