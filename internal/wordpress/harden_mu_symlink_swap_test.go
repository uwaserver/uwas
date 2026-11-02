package wordpress

// Regression test for F63600: Harden checked wp-content/mu-plugins for a
// symlink and then created the directory; a link swapped in between (Install
// re-checks after the mkdir, F54301) made root write or remove
// uwas-security.php through it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hardenMuSwapSetup(t *testing.T, outside string) (webRoot string, restore func()) {
	t.Helper()
	snap := saveHooks()
	webRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "wp-config.php"), []byte("<?php\n/* That's all, stop editing! */\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	osReadFileFn = os.ReadFile
	osWriteFileFn = writeFileNoFollow
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		if strings.HasSuffix(path, filepath.Join("wp-content", "mu-plugins")) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(outside, path); err != nil {
				return err
			}
		}
		return os.MkdirAll(path, perm)
	}
	return webRoot, func() { restoreHooks(snap) }
}

func TestHardenXMLRPCDisableRefusesMuPluginsSwappedAfterCheck(t *testing.T) {
	outside := t.TempDir()
	webRoot, restore := hardenMuSwapSetup(t, outside)
	defer restore()
	on := true
	Harden(webRoot, HardenOptions{DisableXMLRPC: &on})
	if _, err := os.Stat(filepath.Join(outside, "uwas-security.php")); err == nil {
		t.Fatalf("EXPECTED nothing written through the mu-plugins symlink; ACTUAL %s/uwas-security.php exists", outside)
	}
}

func TestHardenXMLRPCEnableDoesNotRemoveThroughSwappedMuPlugins(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "uwas-security.php")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	webRoot, restore := hardenMuSwapSetup(t, outside)
	defer restore()
	off := false
	Harden(webRoot, HardenOptions{DisableXMLRPC: &off})
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("EXPECTED file outside the web root untouched; ACTUAL removed: %v", err)
	}
}
