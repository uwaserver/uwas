package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDomainFileMixedFormatRejected pins that a domains.d / include file mixing
// a top-level single domain with a `domains:` list fails to load instead of
// silently loading only the list. A single-domain reader (the admin raw
// editor) validates the top-level domain, so loading the list instead let a
// non-admin persist fields the editor never checked.
func TestDomainFileMixedFormatRejected(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfgPath := write("uwas.yaml", "global:\n  log_level: info\n  log_format: text\n")
	single := "host: a.test\ntype: static\nroot: /var/www/a\nssl:\n  mode: \"off\"\n"
	list := "domains:\n  - host: a.test\n    type: static\n    root: /var/www/a\n    ssl:\n      mode: \"off\"\n  - host: b.test\n    type: static\n    root: /var/www/b\n    ssl:\n      mode: \"off\"\n"
	domainFile := filepath.Join("domains.d", "a.test.yaml")

	write(domainFile, single)
	if cfg, err := Load(cfgPath); err != nil || len(cfg.Domains) != 1 {
		t.Fatalf("single-domain file: err=%v", err)
	}
	write(domainFile, list)
	if cfg, err := Load(cfgPath); err != nil || len(cfg.Domains) != 2 {
		t.Fatalf("domains-list file: err=%v", err)
	}
	write(domainFile, single+"domains:\n  - host: a.test\n    type: static\n    root: /\n    ssl:\n      mode: \"off\"\n")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "both a top-level host and a domains list") {
		t.Fatalf("mixed file: err=%v, want rejection", err)
	}
}
