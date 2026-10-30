//go:build unix

package phpmanager

import (
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

// F42700: UWAS runs as root (the shipped systemd unit says User=root). The
// php-fpm pool it generates is given an unprivileged user/group (fpmPoolUser),
// but the php-cgi workers StartDomain and StartFPM spawn inherited UWAS's own
// identity, so every tenant's PHP ran as root. The rest of the stack
// (siteuser's user:www-data, WordPress FixPermissions' www-data ownership)
// assumes PHP is the web account.
func TestPHPCGIWorkersDropToTheWebAccountWhenUWASIsRoot(t *testing.T) {
	cur, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	wantUID, _ := strconv.Atoi(cur.Uid)
	wantGID, _ := strconv.Atoi(cur.Gid)

	old := fpmPoolUser
	fpmPoolUser = func() (string, bool) { return cur.Username, true } // "UWAS is root, run workers as this account"
	t.Cleanup(func() { fpmPoolUser = old })

	start := map[string]func(m *Manager) (func(), error){
		"StartDomain": func(m *Manager) (func(), error) {
			if _, err := m.AssignDomain("tenant.test", "8.4"); err != nil {
				return nil, err
			}
			return func() { _ = m.StopDomain("tenant.test") }, m.StartDomain("tenant.test")
		},
		"StartFPM": func(m *Manager) (func(), error) {
			return func() { _ = m.StopFPM("8.4") }, m.StartFPM("8.4", "127.0.0.1:0")
		},
	}
	for name, startFn := range start {
		t.Run(name, func(t *testing.T) {
			m := New(testLogger())
			// No sibling php-fpm binary, so StartFPM takes the php-cgi path.
			m.installations = []PHPInstall{{Version: "8.4.19", Binary: filepath.Join(t.TempDir(), "php-cgi"), SAPI: "cgi-fcgi"}}
			var started *exec.Cmd
			m.execCommand = func(string, ...string) *exec.Cmd {
				started = exec.Command("sleep", "100")
				return started
			}
			stop, err := startFn(m)
			if started == nil {
				t.Fatalf("no worker was created (start error: %v)", err)
			}
			if err == nil {
				t.Cleanup(stop)
			}
			var cred *struct{ uid, gid uint32 }
			if started.SysProcAttr != nil && started.SysProcAttr.Credential != nil {
				c := started.SysProcAttr.Credential
				cred = &struct{ uid, gid uint32 }{c.Uid, c.Gid}
			}
			t.Logf("EXPECTED: credential uid=%d gid=%d (the web account)\nACTUAL: %+v", wantUID, wantGID, cred)
			if cred == nil {
				t.Fatalf("PROBLEM CONFIRMED: %s starts php-cgi with UWAS's own identity although the web account is %q", name, cur.Username)
			}
			if int(cred.uid) != wantUID || int(cred.gid) != wantGID {
				t.Fatalf("worker credential = %+v, want uid=%d gid=%d", *cred, wantUID, wantGID)
			}
			if err != nil {
				t.Fatalf("start: %v", err)
			}
		})
	}
}

// Control: UWAS not running as root (fpmPoolUser reports ok=false, as in the
// container image's non-root uwas user) leaves the worker identity alone.
func TestPHPCGIWorkersKeepIdentityWhenUWASIsNotRoot(t *testing.T) {
	old := fpmPoolUser
	fpmPoolUser = func() (string, bool) { return "", false }
	t.Cleanup(func() { fpmPoolUser = old })

	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: filepath.Join(t.TempDir(), "php-cgi"), SAPI: "cgi-fcgi"}}
	var started *exec.Cmd
	m.execCommand = func(string, ...string) *exec.Cmd {
		started = exec.Command("sleep", "100")
		return started
	}
	if _, err := m.AssignDomain("tenant.test", "8.4"); err != nil {
		t.Fatal(err)
	}
	if err := m.StartDomain("tenant.test"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	t.Cleanup(func() { _ = m.StopDomain("tenant.test") })
	if started.SysProcAttr != nil && started.SysProcAttr.Credential != nil {
		t.Errorf("a non-root UWAS must not set a worker credential, got %+v", started.SysProcAttr.Credential)
	}
}
