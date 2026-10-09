package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateWPConfigDBEscapesCredentials pins the credential-escaping
// contract: DB credentials are written into PHP single-quoted strings, so
// backslashes must be doubled and apostrophes escaped — a password ending
// in a backslash otherwise escapes the closing quote and corrupts
// wp-config.php. Mirrors updateWPConfigURLs in clone.go.
func TestUpdateWPConfigDBEscapesCredentials(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name string
		pass string
	}{
		{name: "apostrophe and trailing backslash", pass: `pa'ss\`},
		{name: "plain password control", pass: "normalpass"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wp := filepath.Join(dir, "wp-config-"+tc.name+".php")
			original := "<?php\ndefine('DB_NAME', 'old');\ndefine('DB_USER', 'old');\ndefine('DB_PASSWORD', 'old');\n"
			if err := os.WriteFile(wp, []byte(original), 0644); err != nil {
				t.Fatal(err)
			}

			log := &strings.Builder{}
			updateWPConfigDB(wp, "example", "migrate_user", tc.pass, log)

			got, err := os.ReadFile(wp)
			if err != nil {
				t.Fatal(err)
			}

			// The documented escaping: backslashes doubled first, then
			// apostrophes escaped — exactly what clone.go's
			// updateWPConfigURLs does for its values.
			escaped := strings.ReplaceAll(tc.pass, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `'`, `\'`)
			want := "define('DB_PASSWORD', '" + escaped + "');"
			if !strings.Contains(string(got), want) {
				t.Fatalf("DB_PASSWORD not written PHP-safe\nwant line containing: %s\ngot:\n%s", want, got)
			}
		})
	}
}
