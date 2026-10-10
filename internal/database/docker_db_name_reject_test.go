package database

import (
	"os/exec"
	"strings"
	"testing"
)

// Docker import/export used to strip invalid characters from the database
// name, so the name a caller checked was not the name the client received:
// "mysql." targeted the `mysql` schema, "shop.prod" targeted `shopprod`, and a
// leading '-' reached the client as an option. Invalid names must be rejected
// before any docker command runs; valid names must pass through unchanged.
func TestDockerDBImportExportRejectInvalidNames(t *testing.T) {
	saveDockerHook(t)
	var calls [][]string
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, args)
		return fakeDockerCmd("", 0)(name, args...)
	}

	for _, db := range []string{"mysql.", "shop.prod", "-hother", "--force", "a;rm", "", strings.Repeat("x", 65)} {
		calls = nil
		if err := DockerDBImport("web", db, "x"); err == nil || len(calls) != 0 {
			t.Errorf("DockerDBImport(%q): err=%v docker calls=%d, want rejection and no docker call", db, err, len(calls))
		}
		calls = nil
		if _, err := DockerDBExport("web", db); err == nil || len(calls) != 0 {
			t.Errorf("DockerDBExport(%q): err=%v docker calls=%d, want rejection and no docker call", db, err, len(calls))
		}
	}

	for _, db := range []string{"shop", "my-db", "a_b", strings.Repeat("x", 64)} {
		calls = nil
		if err := DockerDBImport("web", db, "x"); err != nil || len(calls) != 1 || !strings.HasSuffix(calls[0][len(calls[0])-1], " "+db+"; fi") {
			t.Errorf("DockerDBImport(%q): err=%v calls=%v, want one call targeting %q", db, err, calls, db)
		}
		calls = nil
		if _, err := DockerDBExport("web", db); err != nil || len(calls) != 1 || !strings.HasSuffix(calls[0][len(calls[0])-1], " "+db+"; fi") {
			t.Errorf("DockerDBExport(%q): err=%v calls=%v, want one call targeting %q", db, err, calls, db)
		}
	}

	calls = nil
	if err := DockerDBImport("web", "--all-databases", "x"); err == nil || len(calls) != 0 {
		t.Errorf("DockerDBImport(--all-databases): err=%v calls=%d, want rejection", err, len(calls))
	}
}
