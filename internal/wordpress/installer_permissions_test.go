package wordpress

// Regression: setWordPressPermissions returns nothing, so the log it writes is
// the installer's only feedback about ownership. Every chown/chmod result used
// to be discarded and "Permissions set (www-data:www-data, 755/644, …)" was
// written unconditionally — so a host where the calls failed (UWAS running
// unprivileged, or a filesystem that refuses the mode) reported success while
// the web root stayed owned by the installing user. www-data runs the site, so
// it could not install plugins, upload media, or update, and the operator read
// a success line that was false.
//
// The pre-existing TestSetWordPressPermissions_Linux uses fakeCmd(""), a fake
// that succeeds, so it pinned only the success path.

import (
	"strings"
	"testing"
)

func TestSetWordPressPermissionsDoesNotClaimSuccessOnFailure(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmdFail("Operation not permitted")

	var log strings.Builder
	setWordPressPermissions(t.TempDir(), &log)

	got := log.String()
	if strings.Contains(got, "Permissions set") {
		t.Errorf("log claimed %q although every chown/chmod failed; the operator "+
			"would believe the site is correctly owned while www-data cannot write. got %q",
			"Permissions set", got)
	}
	if !strings.Contains(got, "failed") {
		t.Errorf("log did not report the failures at all: %q", got)
	}
}

func TestSetWordPressPermissionsReportsSuccessWhenCommandsWork(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmd("")

	var log strings.Builder
	setWordPressPermissions(t.TempDir(), &log)

	if !strings.Contains(log.String(), "Permissions set") {
		t.Errorf("expected the success line when every chown/chmod works, got %q", log.String())
	}
	if strings.Contains(log.String(), "failed") {
		t.Errorf("success path must not emit a failure warning: %q", log.String())
	}
}
