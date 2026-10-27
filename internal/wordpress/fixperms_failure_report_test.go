package wordpress

// F31200: FixPermissions printed "Owner set to www-data:www-data", "wp-content
// set to 775", "wp-config.php set to 600" and "Added FS_METHOD ..." whether or
// not chown/chmod/the wp-config write succeeded. On a host where the account
// does not exist (apache/nginx instead of www-data), or UWAS runs unprivileged,
// the "Fix permissions" action reported success while changing nothing. The
// installer's setWordPressPermissions already counts failures; this mirrors it.

import (
	"errors"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
)

const wpConfigComplete = "<?php\ndefine('FS_METHOD', 'direct');\ndefine('WP_TEMP_DIR', __DIR__ . '/.tmp');\nrequire_once ABSPATH . 'wp-settings.php';\n"

// failOnly runs every command successfully except the named one.
func failOnly(failName string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		if name == failName {
			return exec.Command("__nonexistent_binary_for_test__")
		}
		return fakeOutputCmd("", "", 0)
	}
}

func TestFixPermissionsReportsFailedChown(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	root := seedWPConfig(t, wpConfigComplete)

	// Control: everything succeeds.
	execCommandFn = failOnly("")
	okOut, err := FixPermissions(root)
	if err != nil {
		t.Fatalf("control FixPermissions: %v", err)
	}
	if !strings.Contains(okOut, "Owner set to www-data:www-data") || strings.Contains(strings.ToLower(okOut), "warning") {
		t.Fatalf("control output wrong:\n%s", okOut)
	}

	// Defect: chown fails.
	execCommandFn = failOnly("chown")
	out, err := FixPermissions(root)
	t.Logf("EXPECTED: no \"Owner set\" claim and a warning\nACTUAL:\n%s", out)
	if err != nil {
		t.Fatalf("FixPermissions returned error (output-only reporting expected): %v", err)
	}
	if strings.Contains(out, "Owner set to www-data:www-data") {
		t.Errorf("PROBLEM CONFIRMED: chown failed but output claims \"Owner set to www-data:www-data\"")
	}
	if !strings.Contains(strings.ToLower(out), "warning") {
		t.Errorf("PROBLEM CONFIRMED: chown failure is not reported in the output")
	}
}

func TestFixPermissionsReportsFailedChmodAndConfigWrite(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	// chmod fails: wp-content/775 and wp-config.php/600 claims must not appear.
	root := seedWPConfig(t, wpConfigComplete)
	execCommandFn = failOnly("chmod")
	out, _ := FixPermissions(root)
	if strings.Contains(out, "wp-content set to 775") || strings.Contains(out, "wp-config.php set to 600") {
		t.Errorf("PROBLEM CONFIRMED: chmod failed but output claims success:\n%s", out)
	}

	// wp-config.php rewrite fails: "Added ..." must not appear.
	root = seedWPConfig(t, "<?php\nrequire_once ABSPATH . 'wp-settings.php';\n")
	execCommandFn = failOnly("")
	osWriteFileFn = func(string, []byte, fs.FileMode) error { return errors.New("read-only file system") }
	out, _ = FixPermissions(root)
	if strings.Contains(out, "Added FS_METHOD") {
		t.Errorf("PROBLEM CONFIRMED: wp-config.php write failed but output claims it was added:\n%s", out)
	}
	if !strings.Contains(out, "read-only file system") {
		t.Errorf("PROBLEM CONFIRMED: wp-config.php write error is not reported:\n%s", out)
	}
}
