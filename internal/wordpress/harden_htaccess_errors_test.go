package wordpress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wpSiteWithConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfg := "<?php\ndefine('DB_NAME','x');\nrequire_once ABSPATH . 'wp-settings.php';\n"
	if err := os.WriteFile(filepath.Join(root, "wp-config.php"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

// A .htaccess that cannot be read or rewritten (a planted symlink, an
// oversized file) must make Harden fail instead of reporting that directory
// listing was blocked while nothing changed — or, on a read failure, replacing
// the whole file with just the Options line.
func TestHardenBlockDirListingReportsHtaccessErrors(t *testing.T) {
	t.Run("regular file works", func(t *testing.T) {
		root := wpSiteWithConfig(t)
		ht := filepath.Join(root, ".htaccess")
		os.WriteFile(ht, []byte("RewriteEngine On\n"), 0644)
		out, err := Harden(root, HardenOptions{BlockDirListing: boolPtr(true)})
		if err != nil {
			t.Fatalf("Harden: %v", err)
		}
		got, _ := os.ReadFile(ht)
		if !strings.HasPrefix(string(got), "Options -Indexes") || !strings.Contains(string(got), "RewriteEngine On") {
			t.Fatalf("htaccess = %q (out %q)", got, out)
		}
	})
	t.Run("missing file is created", func(t *testing.T) {
		root := wpSiteWithConfig(t)
		if _, err := Harden(root, HardenOptions{BlockDirListing: boolPtr(true)}); err != nil {
			t.Fatalf("Harden: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(root, ".htaccess"))
		if err != nil || !strings.Contains(string(got), "-Indexes") {
			t.Fatalf("htaccess = %q err=%v", got, err)
		}
	})
	for _, block := range []bool{true, false} {
		name := "symlink refused, block=false"
		if block {
			name = "symlink refused, block=true"
		}
		t.Run(name, func(t *testing.T) {
			root := wpSiteWithConfig(t)
			victim := filepath.Join(t.TempDir(), "victim")
			os.WriteFile(victim, []byte("keep\n"), 0644)
			if err := os.Symlink(victim, filepath.Join(root, ".htaccess")); err != nil {
				t.Skip(err)
			}
			out, err := Harden(root, HardenOptions{BlockDirListing: boolPtr(block)})
			if err == nil {
				t.Fatalf("Harden reported success although .htaccess was not written: %q", out)
			}
			if got, _ := os.ReadFile(victim); string(got) != "keep\n" {
				t.Fatalf("victim changed: %q", got)
			}
		})
	}
	t.Run("oversized file is not replaced", func(t *testing.T) {
		root := wpSiteWithConfig(t)
		ht := filepath.Join(root, ".htaccess")
		big := strings.Repeat("# rule\n", maxReadFileSize/7+10)
		os.WriteFile(ht, []byte(big), 0644)
		_, err := Harden(root, HardenOptions{BlockDirListing: boolPtr(true)})
		got, _ := os.ReadFile(ht)
		if len(got) != len(big) {
			t.Fatalf("existing .htaccess was replaced (%d bytes, was %d), err=%v", len(got), len(big), err)
		}
		if err == nil {
			t.Fatalf("expected an error for an unreadable .htaccess")
		}
	})
}
