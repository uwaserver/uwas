package migrate

import (
	"os/exec"
	"strings"
	"testing"
)

// fakeMigrateExec installs execLookPathFn/execCommandFn stand-ins and returns a
// restore func plus a flag recording whether the CREATE USER call was reached.
// That call is the one issued as `bin -u root` with the SQL on stdin, so it is
// distinguishable by argument shape alone.
func fakeMigrateExec(failCreateUser bool) (restore func(), sawCreateUser *bool) {
	origLook, origExec := execLookPathFn, execCommandFn
	saw := false
	execLookPathFn = func(n string) (string, error) { return "/fake/" + n, nil }
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) == 2 && args[0] == "-u" && args[1] == "root" {
			saw = true
			if failCreateUser {
				return exec.Command("sh", "-c", "exit 1")
			}
		}
		return exec.Command("sh", "-c", "exit 0")
	}
	return func() { execLookPathFn, execCommandFn = origLook, origExec }, &saw
}

// Cloning a site must not report success when the database user it was asked
// to provision could not be created. The caller gets a green clone and a site
// that cannot connect to its database, with the failure only visible as a
// runtime "error establishing a database connection".
func TestCloneDBReportsFailedUserCreation(t *testing.T) {
	restore, saw := fakeMigrateExec(true)
	defer restore()

	err := cloneDBReal("srcdb", "dstdb", "appuser", "s3cr3t", &strings.Builder{})
	if !*saw {
		t.Fatal("the CREATE USER path was not exercised, so this run proves nothing")
	}
	if err == nil {
		t.Fatal("cloneDBReal returned nil although CREATE USER for \"appuser\" failed; " +
			"the clone reports success while the requested database user was never created")
	}
	if !strings.Contains(err.Error(), "appuser") {
		t.Errorf("error %q does not name the user that could not be provisioned", err)
	}
}

// The GRANT is a separate statement against that user; if it fails the user
// exists but owns nothing, which fails just as loudly at connect time.
func TestCloneDBReportsFailedGrant(t *testing.T) {
	restore, _ := fakeMigrateExec(false)
	defer restore()

	origExec := execCommandFn
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		// GRANT is issued with -e and a SQL string containing the privilege list.
		if len(args) == 4 && args[2] == "-e" && strings.Contains(args[3], "GRANT") {
			return exec.Command("sh", "-c", "exit 1")
		}
		return origExec(name, args...)
	}

	err := cloneDBReal("srcdb", "dstdb", "appuser", "s3cr3t", &strings.Builder{})
	if err == nil {
		t.Fatal("cloneDBReal returned nil although GRANT to \"appuser\" failed; " +
			"the user is left owning no databases")
	}
	if !strings.Contains(err.Error(), "appuser") {
		t.Errorf("error %q does not name the user that was not granted privileges", err)
	}
}

// Control: when every statement succeeds the clone must still report success.
func TestCloneDBSucceedsWhenProvisioningWorks(t *testing.T) {
	restore, saw := fakeMigrateExec(false)
	defer restore()

	if err := cloneDBReal("srcdb", "dstdb", "appuser", "s3cr3t", &strings.Builder{}); err != nil {
		t.Fatalf("cloneDBReal = %v, want nil when every statement succeeds", err)
	}
	if !*saw {
		t.Fatal("the CREATE USER path was not exercised")
	}
}

// Control: cloning without credentials skips provisioning entirely and must
// still succeed — the new checks must not require a user to be requested.
func TestCloneDBSucceedsWithoutUser(t *testing.T) {
	restore, saw := fakeMigrateExec(false)
	defer restore()

	if err := cloneDBReal("srcdb", "dstdb", "", "", &strings.Builder{}); err != nil {
		t.Fatalf("cloneDBReal = %v, want nil when no user was requested", err)
	}
	if *saw {
		t.Error("CREATE USER ran even though no user was requested")
	}
}

// Control: the existing identifier validation must still reject a malformed
// user before any command runs.
func TestCloneDBStillValidatesUser(t *testing.T) {
	restore, _ := fakeMigrateExec(false)
	defer restore()

	if err := cloneDBReal("srcdb", "dstdb", "bad`user", "pw", &strings.Builder{}); err == nil {
		t.Fatal("an invalid database user was accepted")
	}
}
