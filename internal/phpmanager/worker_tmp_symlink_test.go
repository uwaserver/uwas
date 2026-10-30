//go:build unix

package phpmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F44000: a root UWAS hands the per-domain session directory to the PHP worker
// account. The directory lives in the tenant-writable web root, so a tenant who
// replaced ".tmp" with a symlink to a host directory made the root chown (which
// follows symlinks) give that directory to the account every tenant's PHP runs
// as — and owning a directory is enough to replace anything inside it.
func TestSessionDirSymlinkIsNeverHandedToTheWorkerAccount(t *testing.T) {
	asWebAccount(t)
	victim := t.TempDir() // stands in for /etc or another tenant's root
	root := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(root, ".tmp")); err != nil {
		t.Fatal(err)
	}

	var reached []string
	old := osChownHook
	osChownHook = func(path string, u, g int) error {
		// os.Chown follows symlinks: record where the call would land.
		if real, err := filepath.EvalSymlinks(path); err == nil {
			reached = append(reached, real)
		}
		return nil
	}
	t.Cleanup(func() { osChownHook = old })

	m := identityManager(t, root)
	ini, err := m.buildDomainINI("tenant.test", PHPInstall{}, map[string]string{"memory_limit": "128M"})
	if err == nil {
		t.Cleanup(func() { os.Remove(ini) })
	}

	realVictim, _ := filepath.EvalSymlinks(victim)
	for _, p := range reached {
		if p == realVictim || strings.HasPrefix(p, realVictim+string(filepath.Separator)) {
			t.Fatalf("PROBLEM CONFIRMED: chown reached %s through the .tmp symlink in the tenant's web root", p)
		}
	}
	t.Logf("EXPECTED: no chown outside the web root\nACTUAL: chown targets %v", reached)
}

// Control: a real directory is still handed over.
func TestSessionDirRealDirectoryIsStillHandedToTheWorkerAccount(t *testing.T) {
	asWebAccount(t)
	root := t.TempDir()
	var got []string
	old := osChownHook
	osChownHook = func(path string, u, g int) error { got = append(got, path); return nil }
	t.Cleanup(func() { osChownHook = old })

	m := identityManager(t, root)
	ini, err := m.buildDomainINI("tenant.test", PHPInstall{}, map[string]string{"memory_limit": "128M"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(ini) })
	want := filepath.Join(root, ".tmp")
	for _, p := range got {
		if p == want {
			return
		}
	}
	t.Errorf("%s was not handed to the worker account (chown calls: %v)", want, got)
}
