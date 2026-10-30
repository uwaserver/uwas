package config

import (
	"os"
	"path/filepath"
	"testing"
)

// F56302: an editor's lock/swap artefact in domains.d (Emacs leaves a dangling
// ".#name.yaml" symlink next to the file being edited) used to fail the whole
// load, so a panel reload or restart refused to run while someone had a
// domain file open. Dot-prefixed files are not domain files.
func TestLoadIgnoresDotFilesInDomainsDir(t *testing.T) {
	dir := t.TempDir()
	dd := filepath.Join(dir, "domains.d")
	if err := os.MkdirAll(dd, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "uwas.yaml"), []byte("global:\n  http_listen: \":8080\"\n"), 0600)
	os.WriteFile(filepath.Join(dd, "a.yaml"), []byte("host: a.example.com\nroot: /tmp\ntype: static\n"), 0600)
	if err := os.Symlink("user@host.1234:5678", filepath.Join(dd, ".#a.yaml")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	os.WriteFile(filepath.Join(dd, ".b.yaml"), []byte("host: a.example.com\nroot: /tmp\ntype: static\n"), 0600)

	cfg, err := Load(filepath.Join(dir, "uwas.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Domains) != 1 || cfg.Domains[0].Host != "a.example.com" {
		t.Fatalf("domains = %+v, want only a.example.com", cfg.Domains)
	}
}

// Control: a visible broken file still fails the load (fail closed).
func TestLoadStillFailsOnBrokenDomainFile(t *testing.T) {
	dir := t.TempDir()
	dd := filepath.Join(dir, "domains.d")
	os.MkdirAll(dd, 0755)
	os.WriteFile(filepath.Join(dir, "uwas.yaml"), []byte("global:\n  http_listen: \":8080\"\n"), 0600)
	os.WriteFile(filepath.Join(dd, "a.yaml"), []byte("host: [unclosed\n"), 0600)
	if _, err := Load(filepath.Join(dir, "uwas.yaml")); err == nil {
		t.Fatal("broken domain file loaded")
	}
}
