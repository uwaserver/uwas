package migrate

// Regression: migrateDBReal discarded the results of CREATE USER and GRANT,
// while the import immediately below was checked and reported
// "error: import failed".
//
// The import runs as `-u root`, so it succeeds whether or not the application
// database user exists. Discarding the provisioning errors meant a migration
// could return "ok" with no database user for the destination site to connect
// as — the site came up unable to reach its database, reported as a success.
//
// migrateDBReal had no test coverage before this. Call sites are identified by
// argument shape rather than SQL text, because CREATE USER carries its SQL on
// stdin (`bin -u root`) so the password never appears in argv.

import (
	"os/exec"
	"strings"
	"testing"
)

type siteMigrateCalls struct {
	createUser int
	grant      int
	importDB   int
}

func siteMigrateIsCreateUser(args []string) bool {
	return len(args) == 2 && args[0] == "-u" && args[1] == "root"
}

func siteMigrateIsImport(args []string) bool {
	return len(args) == 3 && args[0] == "-u" && args[1] == "root"
}

func fakeSiteMigrateExec(failCreateUser, failGrant bool, seen *siteMigrateCalls) func(string, ...string) *exec.Cmd {
	fail := func() *exec.Cmd {
		return exec.Command("sh", "-c", "echo 'ERROR 1045 (28000): Access denied' >&2; exit 1")
	}
	ok := func(script string) *exec.Cmd { return exec.Command("sh", "-c", script) }
	return func(_ string, args ...string) *exec.Cmd {
		joined := strings.Join(args, " ")
		switch {
		case siteMigrateIsCreateUser(args):
			seen.createUser++
			if failCreateUser {
				return fail()
			}
			return ok("printf 'ok'")
		case siteMigrateIsImport(args):
			seen.importDB++
			return ok("printf ''")
		case strings.Contains(joined, "GRANT ALL PRIVILEGES"):
			seen.grant++
			if failGrant {
				return fail()
			}
			return ok("printf 'ok'")
		case strings.Contains(joined, "CREATE DATABASE"):
			return ok("printf 'ok'")
		case strings.Contains(joined, "mysqldump"):
			return ok("printf 'CREATE TABLE `t` (id int);\\n'")
		default:
			return ok("printf 'ok'")
		}
	}
}

func installSiteMigrateStubs(t *testing.T, failCreateUser, failGrant bool) *siteMigrateCalls {
	t.Helper()
	origExec, origLook := execCommandFn, execLookPathFn
	t.Cleanup(func() { execCommandFn, execLookPathFn = origExec, origLook })
	seen := &siteMigrateCalls{}
	execLookPathFn = func(string) (string, error) { return "/usr/bin/mariadb", nil }
	execCommandFn = fakeSiteMigrateExec(failCreateUser, failGrant, seen)
	return seen
}

func siteMigrateReq(t *testing.T) MigrateRequest {
	t.Helper()
	return MigrateRequest{
		SourceHost: "10.0.0.5",
		SourcePort: "22",
		DBName:     "shopdb",
		DBUser:     "shopuser",
		DBPass:     "hunter2",
		LocalRoot:  t.TempDir(),
	}
}

func TestMigrateDBReportsFailedUserCreation(t *testing.T) {
	seen := installSiteMigrateStubs(t, true, true)

	var log strings.Builder
	if got := migrateDBReal(siteMigrateReq(t), &log); got == "ok" {
		t.Fatalf("migrateDBReal reported success although CREATE USER failed: the " +
			"import runs as -u root, so it succeeds regardless of whether the " +
			"application user exists")
	}
	// Only CREATE USER is required to be reached: once provisioning failures are
	// reported the function returns early, so GRANT/import are correctly skipped.
	if seen.createUser == 0 {
		t.Fatalf("setup error: CREATE USER call site not reached (createUser=%d)", seen.createUser)
	}
}

func TestMigrateDBReportsFailedGrant(t *testing.T) {
	seen := installSiteMigrateStubs(t, false, true)

	if got := migrateDBReal(siteMigrateReq(t), &strings.Builder{}); got == "ok" {
		t.Fatalf("migrateDBReal reported success although GRANT failed; the user " +
			"exists but owns no databases")
	}
	if seen.grant == 0 {
		t.Fatalf("setup error: GRANT call site not reached")
	}
}

func TestMigrateDBSucceedsWhenUserProvisioningWorks(t *testing.T) {
	seen := installSiteMigrateStubs(t, false, false)

	if got := migrateDBReal(siteMigrateReq(t), &strings.Builder{}); got != "ok" {
		t.Fatalf("expected \"ok\" when every statement works, got %q", got)
	}
	if seen.createUser == 0 || seen.grant == 0 {
		t.Fatalf("control must exercise both provisioning call sites, got createUser=%d grant=%d",
			seen.createUser, seen.grant)
	}
}

func TestMigrateDBStillRequiresExplicitUser(t *testing.T) {
	req := MigrateRequest{SourcePort: "22", DBName: "shopdb", DBUser: "", LocalRoot: t.TempDir()}
	if got := migrateDBReal(req, &strings.Builder{}); got == "ok" {
		t.Fatal("an empty DBUser must still be rejected before any provisioning")
	}
}
