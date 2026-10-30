//go:build unix

package phpmanager

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type chownCall struct {
	path     string
	uid, gid int
}

func identityManager(t *testing.T, webRoot string) *Manager {
	t.Helper()
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: filepath.Join(t.TempDir(), "php-cgi"), SAPI: "cgi-fcgi"}}
	m.execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sleep", "100") }
	if _, err := m.AssignDomainWithRoot("tenant.test", "8.4", webRoot); err != nil {
		t.Fatalf("AssignDomainWithRoot: %v", err)
	}
	return m
}

func asWebAccount(t *testing.T) (uid, gid int) {
	t.Helper()
	cur, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	uid, _ = strconv.Atoi(cur.Uid)
	gid, _ = strconv.Atoi(cur.Gid)
	old := fpmPoolUser
	fpmPoolUser = func() (string, bool) { return cur.Username, true }
	t.Cleanup(func() { fpmPoolUser = old })
	return uid, gid
}

// F42700: once the worker drops to the web account it reads its ini file and
// writes sessions as that account, so UWAS (root) hands both over.
func TestWorkerAccountOwnsItsIniFileAndSessionDir(t *testing.T) {
	uid, gid := asWebAccount(t)
	var calls []chownCall
	old := osChownHook
	osChownHook = func(path string, u, g int) error {
		calls = append(calls, chownCall{path, u, g})
		return nil
	}
	t.Cleanup(func() { osChownHook = old })

	root := t.TempDir()
	m := identityManager(t, root)
	ini, err := m.buildDomainINI("tenant.test", PHPInstall{}, map[string]string{"memory_limit": "128M"})
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	t.Cleanup(func() { os.Remove(ini) })

	var gotINI, gotTmp bool
	for _, c := range calls {
		if c.uid != uid || c.gid != gid {
			t.Errorf("chown %s to %d:%d, want %d:%d", c.path, c.uid, c.gid, uid, gid)
		}
		gotINI = gotINI || c.path == ini
		gotTmp = gotTmp || c.path == filepath.Join(root, ".tmp")
	}
	if !gotINI {
		t.Errorf("the generated ini %s was not handed to the worker account (calls: %+v)", ini, calls)
	}
	if !gotTmp {
		t.Errorf("the session directory %s was not handed to the worker account (calls: %+v)", filepath.Join(root, ".tmp"), calls)
	}
}

// An ini file the worker cannot read is skipped with a warning and PHP starts
// without the sandbox directives; the start must fail instead.
func TestIniChownFailureAbortsInsteadOfStartingUnsandboxed(t *testing.T) {
	asWebAccount(t)
	old := osChownHook
	osChownHook = func(string, int, int) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { osChownHook = old })

	m := identityManager(t, t.TempDir())
	ini, err := m.buildDomainINI("tenant.test", PHPInstall{}, map[string]string{"memory_limit": "128M"})
	if err == nil {
		os.Remove(ini)
		t.Fatal("buildDomainINI succeeded although the worker account cannot read the file")
	}
	if ini != "" {
		t.Errorf("a path %q was returned with the error", ini)
	}
}

// A root UWAS that cannot resolve the web account must not fall back to
// running tenant PHP as root.
func TestRootUWASRefusesToRunPHPWithoutAWebAccount(t *testing.T) {
	old := fpmPoolUser
	fpmPoolUser = func() (string, bool) { return "uwas-no-such-account-f42700", true }
	t.Cleanup(func() { fpmPoolUser = old })

	m := identityManager(t, t.TempDir())
	started := false
	m.execCommand = func(string, ...string) *exec.Cmd {
		started = true
		return exec.Command("sleep", "100")
	}
	err := m.StartDomain("tenant.test")
	if err == nil || !strings.Contains(err.Error(), "uwas-no-such-account-f42700") {
		t.Fatalf("StartDomain err = %v, want an error naming the missing account", err)
	}
	if started {
		t.Error("a worker was spawned although its account could not be resolved")
	}
	if err := m.StartFPM("8.4", "127.0.0.1:0"); err == nil {
		t.Error("StartFPM php-cgi path started without a resolvable account")
		_ = m.StopFPM("8.4")
	}
}
