package migrate

import (
	"os/exec"
	"strings"
	"testing"
)

// MySQL reads `_` and `%` in a GRANT's database part as LIKE wildcards even
// inside backticks; the migrated user must be granted only the one schema.
func TestMigrateDBGrantEscapesWildcards(t *testing.T) {
	origLook, origExec, origTmp := execLookPathFn, execCommandFn, tempDirFn
	t.Cleanup(func() { execLookPathFn, execCommandFn, tempDirFn = origLook, origExec, origTmp })
	dir := t.TempDir()
	tempDirFn = func() string { return dir }
	execLookPathFn = func(n string) (string, error) { return "/fake/" + n, nil }
	var grant, create string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) == 4 && args[2] == "-e" {
			switch {
			case strings.HasPrefix(args[3], "GRANT"):
				grant = args[3]
			case strings.HasPrefix(args[3], "CREATE DATABASE"):
				create = args[3]
			}
		}
		return exec.Command("sh", "-c", "exit 0")
	}
	if res := migrateDBReal(MigrateRequest{SourceHost: "s", DBName: "wp_shop", DBUser: "u", DBPass: "p"}, &strings.Builder{}); res != "ok" {
		t.Fatalf("migrateDBReal = %q", res)
	}
	if !strings.Contains(grant, "ON `wp\\_shop`.*") {
		t.Errorf("GRANT target not escaped: %q", grant)
	}
	if !strings.Contains(create, "`wp_shop`") {
		t.Errorf("CREATE DATABASE must keep the literal name: %q", create)
	}
}
