package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneSurfacesWPConfigUpdateFailure(t *testing.T) {
	oldFiles, oldDB, oldChown := runCloneFiles, runCloneDB, runCloneChown
	t.Cleanup(func() {
		runCloneFiles, runCloneDB, runCloneChown = oldFiles, oldDB, oldChown
	})

	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "clone")
	victim := filepath.Join(t.TempDir(), "outside-config.php")
	original := "<?php\ndefine('DB_NAME', 'production');\n"
	if err := os.WriteFile(victim, []byte(original), 0600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	runCloneFiles = func(_, dst string, _ *strings.Builder) error {
		return os.Symlink(victim, filepath.Join(dst, "wp-config.php"))
	}
	runCloneDB = func(string, string, string, string, *strings.Builder) error { return nil }
	runCloneChown = func(string) {}

	result := Clone(CloneRequest{
		SourceDomain: "production.example",
		TargetDomain: "staging.example",
		SourceRoot:   source,
		TargetRoot:   target,
		SourceDB:     "production_db",
		TargetDB:     "staging_db",
		DBUser:       "staging_user",
		DBPass:       "staging_secret",
	})
	if result.Error == "" || !strings.Contains(result.Error, "wp-config") {
		t.Fatalf("Clone() status=%q error=%q, want surfaced wp-config update failure", result.Status, result.Error)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != original {
		t.Fatalf("symlink target changed: err=%v content=%q", err, data)
	}
}
