package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A domain an operator keeps in a file the main config pulls in with `include:`
// is in memory like any other, so persistConfig wrote it into domains.d/ too.
// The main file still carries the include, so the next start read the host
// from both places and refused the configuration (duplicate host): any save
// from the panel after that left a server that would not start.
func TestPersistConfigDoesNotDuplicateIncludedDomains(t *testing.T) {
	cases := []struct {
		name       string
		main       string
		includeAt  string
		includeBod string
	}{
		{"include glob, single domain file", "global:\n  web_root: /var/www\ninclude:\n  - sites/*.yaml\n", "sites/app.yaml",
			"host: app.example.com\ntype: static\nroot: /var/www/app\nssl:\n  mode: \"off\"\n"},
		{"include glob, domains list", "global:\n  web_root: /var/www\ninclude:\n  - sites/*.yaml\n", "sites/all.yaml",
			"domains:\n  - host: app.example.com\n    type: static\n    root: /var/www/app\n    ssl:\n      mode: \"off\"\n  - host: other.example.com\n    type: static\n    root: /var/www/other\n    ssl:\n      mode: \"off\"\n"},
		{"domains in the main file itself", "global:\n  web_root: /var/www\ndomains:\n  - host: app.example.com\n    type: static\n    root: /var/www/app\n    ssl:\n      mode: \"off\"\n", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "uwas.yaml")
			if err := os.WriteFile(cfgPath, []byte(c.main), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.includeAt != "" {
				p := filepath.Join(dir, c.includeAt)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(c.includeBod), 0o600); err != nil {
					t.Fatal(err)
				}
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

// Editing or deleting an included domain must act on the file it lives in: the
// edit has to survive a restart and the deleted host must not come back from
// the include file at the next start.
func TestPersistConfigEditsAndDeletesIncludedDomains(t *testing.T) {
	t.Setenv("INC_APP_ROOT", "/var/www/app")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global:\n  web_root: /var/www\ninclude:\n  - sites/*.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sites"), 0o755); err != nil {
		t.Fatal(err)
	}
	list := "# operator notes stay\ndomains:\n  - host: app.example.com\n    type: static\n    root: ${INC_APP_ROOT}\n    ssl:\n      mode: \"off\"\n  # the second site\n  - host: other.example.com\n    type: static\n    root: /var/www/other\n    ssl:\n      mode: \"off\"\n"
	single := "host: solo.example.com\ntype: static\nroot: /var/www/solo\nssl:\n  mode: \"off\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sites", "all.yaml"), []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sites", "solo.yaml"), []byte(single), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("control: initial load: %v", err)
	}
	s := &Server{config: cfg, configPath: cfgPath, logger: logger.New("error", "text")}

	for i := range s.config.Domains {
		switch s.config.Domains[i].Host {
		case "other.example.com":
			s.config.Domains[i].Root = "/var/www/other-new"
		case "solo.example.com":
			s.config.Domains[i].Root = "/var/www/solo-new"
		}
	}
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	again, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("PROBLEM CONFIRMED: reload after an edit fails: %v", err)
	}
	roots := map[string]string{}
	for _, d := range again.Domains {
		roots[d.Host] = d.Root
	}
	if len(roots) != 3 || roots["other.example.com"] != "/var/www/other-new" || roots["solo.example.com"] != "/var/www/solo-new" || roots["app.example.com"] != "/var/www/app" {
		t.Fatalf("PROBLEM CONFIRMED: edits did not survive a restart: %v", roots)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sites", "all.yaml"))
	for _, want := range []string{"operator notes stay", "the second site", "${INC_APP_ROOT}"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("a save lost %q in the included list file:\n%s", want, b)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "domains.d")); err == nil {
		if ents, _ := os.ReadDir(filepath.Join(dir, "domains.d")); len(ents) != 0 {
			t.Errorf("included domains were copied into domains.d: %v", ents)
		}
	}

	// Delete: the host leaves memory, the delete handler removes its files.
	kept := s.config.Domains[:0]
	for _, d := range s.config.Domains {
		if d.Host != "other.example.com" && d.Host != "solo.example.com" {
			kept = append(kept, d)
		}
	}
	s.config.Domains = kept
	s.removeDomainFile("other.example.com")
	s.removeDomainFile("solo.example.com")
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	after, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload after a delete fails: %v", err)
	}
	if len(after.Domains) != 1 || after.Domains[0].Host != "app.example.com" {
		var hosts []string
		for _, d := range after.Domains {
			hosts = append(hosts, d.Host)
		}
		t.Fatalf("PROBLEM CONFIRMED: deleted domains came back from the include files: %v", hosts)
	}
}
