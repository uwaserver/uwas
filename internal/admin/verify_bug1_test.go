package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// TestVerifyBug1RegressionWouldFail confirms our regression test would
// catch the original bug — runs the migration with an EMPTY configPath
// (the bug-1 state) and asserts the on-disk YAML stays as type=app.
// If this passes, our positive test ALSO catching the inverse means
// the assertion is valid.
func TestVerifyBug1RegressionWouldFail(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "x.test.yaml"),
		[]byte("host: x.test\ntype: app\nroot: /tmp\nssl: {mode: \"off\"}\napp: {runtime: node, command: x, port: 9}\n"),
		0600)

	cfg := &config.Config{
		Global:     config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}, WebRoot: dir},
		DomainsDir: domainsDir,
		Domains: []config.Domain{{
			Host: "x.test", Root: "/tmp", Type: "app", SSL: config.SSLConfig{Mode: "off"},
			App: config.AppConfig{Runtime: "node", Command: "x", Port: 9},
		}},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	// Deliberately DO NOT SetConfigPath — simulates Bug #1.
	s.SetAppsManager(apps.NewManager(apps.NewStore(filepath.Join(dir, "apps.d")), nil))

	s.MigrateLegacyAppsAtBoot()

	// Read back: the YAML on disk MUST still be type=app (rewrite skipped).
	data, _ := os.ReadFile(filepath.Join(domainsDir, "x.test.yaml"))
	if !strings.Contains(string(data), "type: app") {
		t.Errorf("expected on-disk YAML to remain type=app with no configPath, got:\n%s", data)
	}
}
