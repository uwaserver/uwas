package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func writeDomainsDFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	d := filepath.Join(dir, "domains.d")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// persistConfig writes every domain to <host>.yaml and never deletes anything.
// A domain that came from a file with another name (an operator's site1.yaml, a
// .yml file, one file listing several domains) was therefore written a second
// time under its host name while the original stayed: the next start read the
// same host from two files and refused the configuration. The same applies to
// deleting a domain, which only looked for <host>.yaml/.yml: the file under
// its other name brought the deleted site back.
func TestPersistConfigDoesNotDuplicateDomainFiles(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"file named differently", map[string]string{"site1.yaml": "host: app.example.com\ntype: static\nroot: /var/www/app\nssl:\n  mode: \"off\"\n"}},
		{"yml extension", map[string]string{"app.example.com.yml": "host: app.example.com\ntype: static\nroot: /var/www/app\nssl:\n  mode: \"off\"\n"}},
		{"several domains in one file", map[string]string{"all.yaml": "domains:\n  - host: app.example.com\n    type: static\n    root: /var/www/app\n    ssl:\n      mode: \"off\"\n  - host: other.example.com\n    type: static\n    root: /var/www/other\n    ssl:\n      mode: \"off\"\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "uwas.yaml")
			if err := os.WriteFile(cfgPath, []byte("global:\n  web_root: /var/www\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for n, b := range c.files {
				writeDomainsDFile(t, dir, n, b)
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("control: initial load: %v", err)
			}
			s := &Server{config: cfg, configPath: cfgPath, logger: logger.New("error", "text")}
			if err := s.persistConfig(); err != nil {
				t.Fatal(err)
			}
			again, err := config.Load(cfgPath)
			t.Logf("EXPECTED: the configuration loads again after a save\nACTUAL: err=%v", err)
			if err != nil {
				t.Fatalf("PROBLEM CONFIRMED: reload after persistConfig fails: %v", err)
			}
			if len(again.Domains) != len(cfg.Domains) {
				t.Fatalf("PROBLEM CONFIRMED: %d domains after reload, want %d", len(again.Domains), len(cfg.Domains))
			}
		})
	}
}

func TestDeleteDomainRemovesItsFileWhateverItIsCalled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global:\n  web_root: /var/www\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeDomainsDFile(t, dir, "site1.yaml", "host: gone.example.com\ntype: static\nroot: /var/www/gone\nssl:\n  mode: \"off\"\n")
	writeDomainsDFile(t, dir, "keep.example.com.yaml", "host: keep.example.com\ntype: static\nroot: /var/www/keep\nssl:\n  mode: \"off\"\n")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{config: cfg, configPath: cfgPath, logger: logger.New("error", "text")}
	// What handleDeleteDomain does: drop the domain from memory, remove its
	// file, save.
	s.configMu.Lock()
	kept := cfg.Domains[:0]
	for _, d := range cfg.Domains {
		if d.Host != "gone.example.com" {
			kept = append(kept, d)
		}
	}
	cfg.Domains = kept
	s.configMu.Unlock()
	s.removeDomainFile("gone.example.com")
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	again, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, d := range again.Domains {
		if d.Host == "gone.example.com" {
			t.Fatal("PROBLEM CONFIRMED: the deleted domain is back after a restart")
		}
	}
	if len(again.Domains) != 1 || again.Domains[0].Host != "keep.example.com" {
		t.Fatalf("control: domains after reload = %+v", again.Domains)
	}
}

func newDomainsDServer(t *testing.T, files map[string]string) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global:\n  web_root: /var/www\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for n, b := range files {
		writeDomainsDFile(t, dir, n, b)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return &Server{config: cfg, configPath: cfgPath, logger: logger.New("error", "text")}, dir, cfgPath
}

// A domain kept in a file of its own is saved into that file; nothing new
// appears next to it and the file keeps its name.
func TestPersistConfigSavesDomainIntoItsExistingFile(t *testing.T) {
	s, dir, cfgPath := newDomainsDServer(t, map[string]string{
		"site1.yaml": "host: app.example.com\ntype: static\nroot: /var/www/app\nssl:\n  mode: \"off\"\n",
	})
	s.configMu.Lock()
	s.config.Domains[0].Root = "/var/www/changed"
	s.configMu.Unlock()
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "domains.d"))
	if len(entries) != 1 || entries[0].Name() != "site1.yaml" {
		t.Fatalf("domains.d = %v, want only site1.yaml", entries)
	}
	again, err := config.Load(cfgPath)
	if err != nil || len(again.Domains) != 1 || again.Domains[0].Root != "/var/www/changed" {
		t.Fatalf("reload = %+v, %v", again.Domains, err)
	}
}

// A list file whose domains were all saved under their host names is retired
// (renamed, not deleted); a file with a host that is not in the live
// configuration is left alone.
func TestPersistConfigRetiresOnlyFullySupersededFiles(t *testing.T) {
	s, dir, _ := newDomainsDServer(t, map[string]string{
		"all.yaml": "domains:\n  - host: a.example.com\n    type: static\n    root: /var/www/a\n    ssl:\n      mode: \"off\"\n  - host: b.example.com\n    type: static\n    root: /var/www/b\n    ssl:\n      mode: \"off\"\n",
	})
	// A file that does not parse names no host, so it cannot be superseded.
	broken := writeDomainsDFile(t, dir, "broken.yaml", "host: [unterminated\n")
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	dd := filepath.Join(dir, "domains.d")
	if _, err := os.Stat(filepath.Join(dd, "all.yaml.superseded")); err != nil {
		t.Fatalf("list file was not retired: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dd, "all.yaml")); err == nil {
		t.Fatal("list file still loaded")
	}
	if _, err := os.Stat(broken); err != nil {
		t.Fatalf("control: an unparsable file must be left alone: %v", err)
	}
	for _, h := range []string{"a.example.com.yaml", "b.example.com.yaml"} {
		if _, err := os.Stat(filepath.Join(dd, h)); err != nil {
			t.Fatalf("control: %s not written: %v", h, err)
		}
	}
}

// Deleting one domain of a list file cuts only that entry out and keeps the
// others, their comments and their ${VAR} references.
func TestDeleteDomainFromListFileKeepsTheRest(t *testing.T) {
	t.Setenv("LIST_ROOT", "/var/www/b")
	s, dir, cfgPath := newDomainsDServer(t, map[string]string{
		"all.yaml": "# managed by hand\ndomains:\n  - host: a.example.com\n    type: static\n    root: /var/www/a\n    ssl:\n      mode: \"off\"\n  - host: b.example.com # keep me\n    type: static\n    root: ${LIST_ROOT}\n    ssl:\n      mode: \"off\"\n",
	})
	s.configMu.Lock()
	kept := s.config.Domains[:0]
	for _, d := range s.config.Domains {
		if d.Host != "a.example.com" {
			kept = append(kept, d)
		}
	}
	s.config.Domains = kept
	s.configMu.Unlock()
	s.removeDomainFile("a.example.com")
	raw, err := os.ReadFile(filepath.Join(dir, "domains.d", "all.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# managed by hand", "# keep me", "${LIST_ROOT}", "b.example.com"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("list file lost %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "a.example.com") {
		t.Errorf("deleted domain still in the list file:\n%s", raw)
	}
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	again, err := config.Load(cfgPath)
	if err != nil || len(again.Domains) != 1 || again.Domains[0].Host != "b.example.com" || again.Domains[0].Root != "/var/www/b" {
		t.Fatalf("reload = %+v, %v", again.Domains, err)
	}
}
