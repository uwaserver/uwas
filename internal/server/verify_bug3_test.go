package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestVerifyBug3WouldRegress: confirms the Bug #3 assertion is sharp.
// If reload() is SKIPPED after migration (the original bug condition),
// the proxy pool table stays as built by New() — empty for the
// migrated host. This test asserts that, proving the regression test
// actually distinguishes "pool exists" from "pool missing".
func TestVerifyBug3WouldRegress(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "x.test.yaml"),
		[]byte("host: x.test\ntype: proxy\nssl: {mode: \"off\"}\nproxy: {upstreams: [{address: \"apps://x\"}]}\n"), 0600)

	// Empty initial config = pool builder skips x.test.
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18190", HTTPSListen: ":18191"},
	}
	s := New(cfg, logger.New("error", "text"))
	s.appsMgr = apps.NewManager(apps.NewStore(filepath.Join(dir, "apps.d")), nil)

	// DON'T call reload — simulates Bug #3 (no post-migration reload).
	if _, exists := s.proxyPools["x.test"]; exists {
		t.Error("without reload, pool MUST be missing — but it exists, meaning the assertion is bogus")
	}
}
