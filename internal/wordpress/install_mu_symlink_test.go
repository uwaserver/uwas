package wordpress

// Regression test for F54301: Install ran as root created wp-content/mu-plugins
// and wrote uwas-rewrite.php through it without the symlink check the
// hardening path (F9200) uses, so a link the site's own code planted while the
// installer was still chowning the tree redirected the write elsewhere.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRefusesSymlinkedMuPlugins(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	webRoot := t.TempDir()
	outside := t.TempDir()

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd("OK\n")
	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake-tar-content")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = writeFileNoFollow
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		if strings.HasSuffix(path, filepath.Join("wp-content", "mu-plugins")) {
			// The site's own code swaps the directory for a link right
			// before the installer (root) uses it.
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(outside, path); err != nil {
				return err
			}
		}
		return os.MkdirAll(path, perm)
	}

	Install(InstallRequest{Domain: "test.com", WebRoot: webRoot})

	if _, err := os.Stat(filepath.Join(outside, "uwas-rewrite.php")); err == nil {
		t.Fatalf("EXPECTED nothing written through the mu-plugins symlink; ACTUAL %s/uwas-rewrite.php exists", outside)
	}
}
