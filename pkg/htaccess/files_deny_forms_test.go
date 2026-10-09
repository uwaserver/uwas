package htaccess

import (
	"strings"
	"testing"
)

func filesDenies(t *testing.T, src, filename string) bool {
	t.Helper()
	directives, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return FilesMatchDenies(Convert(directives), filename)
}

// TestFilesMatchDeniesInsideIfModule pins the Apache 2.2/2.4 dual-syntax
// idiom: the deny lives in <IfModule> blocks nested in <FilesMatch>, and was
// previously ignored, serving the files the operator denied.
func TestFilesMatchDeniesInsideIfModule(t *testing.T) {
	src := `<FilesMatch "\.sql$">
<IfModule mod_authz_core.c>
Require all denied
</IfModule>
<IfModule !mod_authz_core.c>
Order allow,deny
Deny from all
</IfModule>
</FilesMatch>`
	if !filesDenies(t, src, "dump.sql") {
		t.Error("FilesMatchDenies(dump.sql) = false, want true for deny nested in <IfModule>")
	}
	if filesDenies(t, src, "index.html") {
		t.Error("FilesMatchDenies(index.html) = true, want false for non-matching file")
	}

	// Only the inactive 2.2 branch: Apache (authz_core loaded) does not deny.
	inactive := "<FilesMatch \"\\.sql$\">\n<IfModule !mod_authz_core.c>\nDeny from all\n</IfModule>\n</FilesMatch>"
	if filesDenies(t, inactive, "dump.sql") {
		t.Error("deny inside an inactive <IfModule !mod_authz_core.c> must not apply")
	}
}

// TestFilesBlockWildcardAndRegexForms pins <Files> semantics: a shell
// wildcard (or "~ regex"), not a regex. "*.sql" used to fail regex
// compilation and "~" was stored as the pattern — both failed open.
func TestFilesBlockWildcardAndRegexForms(t *testing.T) {
	tests := []struct {
		src, file string
		want      bool
	}{
		{"<Files \"*.sql\">\nRequire all denied\n</Files>", "dump.sql", true},
		{"<Files \"*.sql\">\nRequire all denied\n</Files>", "dump.sql.txt", false},
		{"<Files \"db-?.bak\">\nDeny from all\n</Files>", "db-1.bak", true},
		{"<Files ~ \"\\.sql$\">\nRequire all denied\n</Files>", "dump.sql", true},
		{"<Files \"backup.sql\">\nRequire all denied\n</Files>", "backup.sql", true},
	}
	for _, tt := range tests {
		if got := filesDenies(t, tt.src, tt.file); got != tt.want {
			t.Errorf("%q on %s: FilesMatchDenies = %v, want %v", strings.SplitN(tt.src, "\n", 2)[0], tt.file, got, tt.want)
		}
	}
}
