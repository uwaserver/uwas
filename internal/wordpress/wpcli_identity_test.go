package wordpress

// F43000: wp-cli loads the site's wp-config.php, mu-plugins, plugins and
// themes in-process. A root UWAS ran it as root, so any plugin a tenant could
// drop became root code the next time the operator opened the plugin list.
// These tests capture the command's credential through the exec seam, so they
// need neither root nor a real wp-cli.

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// captureWPCLICmd runs wpCLI with the exec seam recording the command and the
// run identity forced to id (nil = UWAS keeps its own identity).
func captureWPCLICmd(t *testing.T, id *runIdentity, idErr error) (*exec.Cmd, error) {
	t.Helper()
	var got *exec.Cmd
	oldExec, oldID := execCommandFn, wpRunIdentityFn
	t.Cleanup(func() { execCommandFn, wpRunIdentityFn = oldExec, oldID })
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		got = exec.Command("true")
		return got
	}
	wpRunIdentityFn = func(string) (*runIdentity, error) { return id, idErr }
	_, err := wpCLI(t.TempDir(), "plugin", "list")
	return got, err
}

func TestWPCLIRunsAsSiteAccountNotRoot(t *testing.T) {
	// The test process is unprivileged, so the identity is its own: the switch
	// is then permitted and the command really runs.
	id := &runIdentity{uid: os.Getuid(), gid: os.Getgid()}
	cmd, err := captureWPCLICmd(t, id, nil)
	if err != nil {
		t.Fatalf("wpCLI: %v", err)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatalf("EXPECTED: wp-cli started with the site account's credential\nACTUAL: SysProcAttr=%+v\nPROBLEM CONFIRMED", cmd.SysProcAttr)
	}
	c := cmd.SysProcAttr.Credential
	if int(c.Uid) != id.uid || int(c.Gid) != id.gid {
		t.Fatalf("credential = %d:%d, want %d:%d", c.Uid, c.Gid, id.uid, id.gid)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "WP_CLI_CACHE_DIR=") {
		t.Errorf("no private wp-cli cache dir in env: %v", cmd.Env)
	}
	// The scratch HOME is last, so it wins over root's inherited one.
	home := ""
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "HOME=") {
			home = kv
		}
	}
	if home == "HOME=/root" {
		t.Errorf("wp-cli kept root's HOME: %s", home)
	}
}

func TestWPCLIKeepsOwnIdentityWhenNotRoot(t *testing.T) {
	cmd, err := captureWPCLICmd(t, nil, nil)
	if err != nil {
		t.Fatalf("wpCLI: %v", err)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Credential != nil {
		t.Errorf("an unprivileged UWAS must not switch identity, got %+v", cmd.SysProcAttr.Credential)
	}
}

func TestWPCLIRefusesToRunWhenAccountUnresolvable(t *testing.T) {
	_, err := captureWPCLICmd(t, nil, errors.New("no such account"))
	if err == nil || !strings.Contains(err.Error(), "no such account") {
		t.Fatalf("err = %v, want the account error (never fall back to root)", err)
	}
}

func TestChooseRunIdentity(t *testing.T) {
	web := func() (*runIdentity, error) { return &runIdentity{uid: 33, gid: 33}, nil }
	webErr := func() (*runIdentity, error) { return nil, errors.New("no web account") }
	cases := []struct {
		name      string
		euid      int
		owner     [2]int
		known     bool
		web       func() (*runIdentity, error)
		want      *runIdentity
		wantError bool
	}{
		{"unprivileged UWAS keeps its identity", 1000, [2]int{1001, 1001}, true, web, nil, false},
		{"root runs as the site owner", 0, [2]int{1001, 1002}, true, web, &runIdentity{1001, 1002}, false},
		{"root-owned web root falls back to the web account", 0, [2]int{0, 0}, true, web, &runIdentity{33, 33}, false},
		{"unknown owner falls back to the web account", 0, [2]int{}, false, web, &runIdentity{33, 33}, false},
		{"no web account is an error, not root", 0, [2]int{0, 0}, true, webErr, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chooseRunIdentity(c.euid, c.owner[0], c.owner[1], c.known, c.web)
			if (err != nil) != c.wantError {
				t.Fatalf("err = %v, wantError %v", err, c.wantError)
			}
			switch {
			case got == nil && c.want == nil:
			case got == nil || c.want == nil || *got != *c.want:
				t.Fatalf("identity = %+v, want %+v", got, c.want)
			}
		})
	}
}
