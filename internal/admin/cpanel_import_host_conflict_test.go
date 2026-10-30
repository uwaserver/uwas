package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/migrate"
)

// A cPanel import registers its domains as new php sites. createDomainsFromMigration
// only compared the exact host, so an imported hostname that an existing site
// already claims as an alias (or that differs only in case) was appended as a
// second owner. persistConfig then wrote a configuration that config.Load
// rejects ("duplicate host/alias"), so the next restart or reload failed.
func TestCPanelImportSkipsHostsAlreadyClaimed(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{
		{Host: "main.test", Type: "static", Aliases: []string{"shop.test"}},
	})
	webRoot := e.s.config.Global.WebRoot
	result := &migrate.CPanelResult{Domains: []migrate.CPanelDomain{
		{Domain: "shop.test", DocRoot: "public_html/shop", Type: "addon"},
		{Domain: "MAIN.test", DocRoot: "public_html", Type: "addon"},
		{Domain: "fresh.test", DocRoot: "public_html/fresh", Type: "addon"},
	}}

	added, skipped := e.s.createDomainsFromMigrationChecked(result, webRoot)
	t.Logf("EXPECTED: added=[fresh.test] skipped=1 (shop.test names its owner; MAIN.test is the existing site)\nACTUAL: added=%v skipped=%v", added, skipped)
	if len(added) != 1 || added[0] != "fresh.test" {
		t.Fatalf("PROBLEM CONFIRMED: added=%v, want only fresh.test (shop.test is an alias, MAIN.test duplicates main.test)", added)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "shop.test") || !strings.Contains(skipped[0], "main.test") {
		t.Errorf("skipped=%v, want one message naming shop.test and its owner main.test", skipped)
	}

	if err := e.s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(e.cfgPath); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: persisted configuration no longer loads: %v", err)
	}
}

// Control: an unclaimed hostname is still created as a php site rooted under
// the web root.
func TestCPanelImportStillCreatesFreshHost(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "main.test", Type: "static"}})
	webRoot := e.s.config.Global.WebRoot
	result := &migrate.CPanelResult{Domains: []migrate.CPanelDomain{{Domain: "fresh.test", Type: "addon"}}}
	added, skipped := e.s.createDomainsFromMigrationChecked(result, webRoot)
	if len(added) != 1 || len(skipped) != 0 {
		t.Fatalf("added=%v skipped=%v, want fresh.test added and nothing skipped", added, skipped)
	}
	want := filepath.Join(webRoot, "fresh.test", "public_html")
	e.s.configMu.RLock()
	defer e.s.configMu.RUnlock()
	for _, d := range e.s.config.Domains {
		if d.Host == "fresh.test" && d.Root != want {
			t.Errorf("root = %q, want %q", d.Root, want)
		}
	}
}
