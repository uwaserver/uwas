package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestVerifyBugAWouldRegress is the inverse probe for the
// config-pointer-drift regression test. It simulates the BROKEN
// behavior — pointer swap on reload — and asserts that a domain
// appended through the original pointer would be INVISIBLE to the
// server after the swap.
//
// If this test ever fails, it means the failure mode is no longer
// reproducible by a pointer swap, and TestConfigPointerSurvivesReload
// can't actually distinguish between the fix and the bug. That's a
// signal the assertion's sharpness has been lost — investigate before
// removing this test.
func TestVerifyBugAWouldRegress(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	mainCfg := []byte("global:\n  worker_count: \"1\"\n  log_level: error\n  log_format: text\n  http_listen: \":18288\"\n  https_listen: \":18289\"\n  pid_file: \"\"\n  http3: false\n  admin:\n    enabled: false\ndomains: []\n")
	os.WriteFile(cfgPath, mainCfg, 0600)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18288", HTTPSListen: ":18289",
		},
	}
	s := New(cfg, logger.New("error", "text"))
	s.SetConfigPath(cfgPath)

	// Capture the ORIGINAL pointer (this is what admin.New() would
	// have stashed in its own s.config field).
	adminView := s.config

	// Simulate the broken behavior: pointer swap on reload. We can't
	// actually call the real reload() because it now does an in-place
	// copy (the fix). So manually emulate the buggy version.
	newCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s.configMu.Lock()
	s.config = newCfg // <-- BUG-A-SIMULATION: pointer swap, not in-place copy
	s.configMu.Unlock()

	// Now do what admin.handleAddDomain does: append to ITS view of
	// the config — which is now the orphan.
	adminView.Domains = append(adminView.Domains, config.Domain{
		Host: "ghost.test",
		Type: "static",
		Root: dir,
		SSL:  config.SSLConfig{Mode: "off"},
	})

	// Server's vhost router gets the LIVE config (post-swap), not the
	// admin's stale view. The append is invisible.
	s.vhosts.Update(s.config.Domains)

	// The bug is: ghost.test is NOT findable in vhost router, even
	// though "admin" believes it added it.
	if d, configured := s.vhosts.LookupWithStatus("ghost.test"); configured {
		t.Errorf("expected ghost.test to be UNCONFIGURED under simulated Bug A — but found %+v. "+
			"That means the failure mode isn't a pointer swap anymore, and the positive test "+
			"can't actually distinguish the fix.", d)
	}

	// Sanity: server's config has 0 domains, admin's view has 1 — they're orthogonal.
	if len(s.config.Domains) != 0 {
		t.Errorf("server config.Domains = %d, want 0", len(s.config.Domains))
	}
	if len(adminView.Domains) != 1 {
		t.Errorf("admin view Domains = %d, want 1", len(adminView.Domains))
	}
}
