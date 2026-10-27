package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// F35600: the published QuickStart "Migrate" tab appended a second
// `uwas migrate` output to uwas.yaml with `>>`. Every migrate output is a
// document with its own top-level `domains:` key, so the appended file has a
// duplicate key and the server refuses to load it. The documented flow is one
// file per source merged through domains_dir.

var migrateAppend = regexp.MustCompile(`uwas migrate \w+ .*>> *\S*uwas\.ya?ml`)

func TestQuickStartDoesNotAppendMigrateOutputToTheMainConfig(t *testing.T) {
	data, err := os.ReadFile(repoFile(t, "docs/site/src/components/QuickStart.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	if m := migrateAppend.Find(data); m != nil {
		t.Fatalf("QuickStart appends migrate output to the main config, which duplicates the domains key: %q", m)
	}
}

func TestMigratedFilesInDomainsDirLoadTogether(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "domains.d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"nginx.yaml":  "# Converted from Nginx config by UWAS\ndomains:\n  - host: a.example.com\n    type: static\n    root: /var/www/a\n",
		"apache.yaml": "# Converted from Apache config by UWAS\ndomains:\n  - host: b.example.com\n    type: static\n    root: /var/www/b\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(main, []byte("domains_dir: ./domains.d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(main)
	if err != nil {
		t.Fatalf("documented migrate flow does not load: %v", err)
	}
	if len(cfg.Domains) != 2 {
		t.Fatalf("domains = %d, want 2", len(cfg.Domains))
	}

	// Control: the same two documents concatenated into one file are what the
	// old documentation produced; the loader must keep rejecting that.
	cat := filepath.Join(dir, "cat.yaml")
	if err := os.WriteFile(cat, []byte(files["nginx.yaml"]+files["apache.yaml"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cat); err == nil {
		t.Fatal("concatenated migrate outputs loaded; the duplicate-key guard is gone")
	}
}
