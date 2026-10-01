package cronjob

import "testing"

// List routes through readCrontab so it applies the same rule that
// readCrontab's own doc comment states and that TestReadCrontab_Empty and
// TestReadCrontab_TransientFailureAborts already pin for the write path:
//
//   - a genuinely empty crontab (exit 1 whose stderr says "no crontab")
//     is reported as ("", nil) — no jobs, no error;
//   - any other `crontab -l` failure must surface as an error.
//
// Before this, List called runCrontab directly and collapsed `if err != nil`
// into `return nil, nil`, so a permission error, a broken crontab binary or
// spool contention was reported to the admin panel as "you have no cron jobs".
// That also made the `if err != nil` branch in internal/admin/files/handler.go
// (CronList) dead code — evidence the error return was always intended to be
// reachable.

// TestList_SurfacesNonEmptyCrontabFailure is the regression: a `crontab -l`
// failure that is NOT "no crontab" must be reported as an error.
func TestList_SurfacesNonEmptyCrontabFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
	}{
		{name: "permission denied", stderr: "crontab: permission denied\n"},
		{name: "broken binary", stderr: "crontab: not found\n"},
		{name: "spool lock contention", stderr: "crontab: locking /var/spool/cron/crontab: Resource temporarily unavailable\n"},
		{name: "unexpected status", stderr: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origGOOS := runtimeGOOS
			origCmd := execCommandFn
			runtimeGOOS = "linux"
			t.Cleanup(func() {
				runtimeGOOS = origGOOS
				execCommandFn = origCmd
			})

			execCommandFn = fakeExecCommandWithStderr("", tc.stderr, 1)

			jobs, err := List()
			if err == nil {
				t.Fatalf("FAIL: List() returned (jobs=%v, nil) for a crontab failure "+
					"(stderr=%q); a real failure must be reported as an error, not as "+
					"an empty job list", jobs, tc.stderr)
			}
		})
	}
}

// TestList_ReportsGenuinelyEmptyCrontabAsNoJobs is the control: the fix must
// NOT turn a genuinely empty crontab into a 500 in the admin panel. This is
// the behaviour the public contract requires and the one an over-broad fix
// ("always return an error") would break.
func TestList_ReportsGenuinelyEmptyCrontabAsNoJobs(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	t.Cleanup(func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	})

	execCommandFn = fakeExecCommandWithStderr("", "no crontab for user\n", 1)

	jobs, err := List()
	if err != nil {
		t.Fatalf("a genuinely empty crontab must not be an error, got %v", err)
	}
	if jobs != nil {
		t.Fatalf("expected nil jobs for an empty crontab, got %v", jobs)
	}
}
