package wordpress

// F31202: Install creates the database account with CREATE USER IF NOT EXISTS,
// which leaves an existing account — and its password — untouched, yet writes
// the freshly generated password into wp-config.php and reports "done". The
// usual way to hit it is a retry: the account is created before the download,
// so a failed download followed by a second Install (which generates a new
// random password) leaves a site that answers "Error establishing a database
// connection". Same class as database.CreateDatabase (F11900).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mysqlAccountScript answers the existence probe with the given count and
// succeeds for everything else, reading the SQL from stdin like the real client.
func mysqlAccountCmd(count string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c",
			`in=$(cat); case "$in" in *"COUNT(*)"*) echo "$1";; esac; exit 0`, "sh", count)
	}
}

func installWithAccount(t *testing.T, count string, req InstallRequest) (InstallResult, string) {
	t.Helper()
	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	runtimeGOOS = "linux"
	req.WebRoot = t.TempDir()
	req.Domain = "retry.test"
	execLookPathFn = fakeLookPathOK
	execCommandFn = mysqlAccountCmd(count)
	var cleanup func()
	httpGetFn, cleanup = fakeTarHandlerFunc("fake-tar-content")
	t.Cleanup(cleanup)
	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir
	res := Install(req)
	cfg, _ := os.ReadFile(filepath.Join(req.WebRoot, "wp-config.php"))
	return res, string(cfg)
}

func TestInstallRefusesExistingDBUserWithGeneratedPassword(t *testing.T) {
	// Control: no such account — installs normally.
	res, cfg := installWithAccount(t, "0", InstallRequest{})
	if res.Status != "done" || !strings.Contains(cfg, res.DBPass) {
		t.Fatalf("control failed: status=%q err=%q", res.Status, res.Error)
	}

	// Defect: the account exists and its password is unknown to us.
	res, cfg = installWithAccount(t, "1", InstallRequest{})
	t.Logf("EXPECTED: status=error and no wp-config.php carrying an unusable password\nACTUAL: status=%q error=%q wp-config written=%v", res.Status, res.Error, cfg != "")
	if res.Status == "done" {
		t.Fatalf("PROBLEM CONFIRMED: Install reported done with a generated password the existing account does not have")
	}
	if cfg != "" {
		t.Errorf("PROBLEM CONFIRMED: wp-config.php was written despite the refusal")
	}
	if !strings.Contains(res.Error, "already exists") {
		t.Errorf("error should say the account exists, got %q", res.Error)
	}
}

func TestInstallAllowsExistingDBUserWithSuppliedPassword(t *testing.T) {
	// The caller named the password, so reuse of an existing account is theirs
	// to vouch for; this must keep working.
	res, cfg := installWithAccount(t, "1", InstallRequest{DBPass: "caller-known-pass"})
	if res.Status != "done" {
		t.Fatalf("supplied password + existing account must still install: status=%q err=%q", res.Status, res.Error)
	}
	if !strings.Contains(cfg, "caller-known-pass") {
		t.Errorf("wp-config.php should carry the supplied password")
	}
}
