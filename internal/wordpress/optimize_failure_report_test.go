package wordpress

// F37601: OptimizeDatabase swallowed every wp-cli failure. When wp-cli could
// not run at all (not installed, database unreachable, not a WordPress root)
// the endpoint answered 200 with all-zero counters and an empty output, so the
// "Optimize database" button looked like it had worked. A step that fails
// while others succeed was likewise dropped without a word.

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// optimizeCmd answers `post|comment list ... --format=ids` with ids, makes the
// commands for which fail(args) is true exit non-zero, and succeeds otherwise.
func optimizeCmd(ids string, fail func(joined string) bool) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		joined := strings.Join(args, " ")
		if fail != nil && fail(joined) {
			return fakeOutputCmd("", "Error: boom", 1)
		}
		out := ""
		if strings.Contains(joined, "list") && strings.Contains(joined, "format=ids") {
			out = ids
		}
		return fakeOutputCmd(out, "", 0)
	}
}

func TestOptimizeDatabaseReportsWPCLIUnavailable(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	execCommandFn = fakeCmdFail("")
	res, err := OptimizeDatabase(t.TempDir())
	t.Logf("EXPECTED: error when no wp-cli step ran\nACTUAL: err=%v res=%+v", err, res)
	if err == nil {
		t.Fatalf("PROBLEM CONFIRMED: every wp-cli call failed but OptimizeDatabase returned success: %+v", res)
	}
}

func TestOptimizeDatabaseReportsFailedSteps(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	// The transient purge and the table optimisation fail, everything else works.
	execCommandFn = optimizeCmd("", func(j string) bool {
		return strings.Contains(j, "transient") || strings.Contains(j, "db optimize")
	})
	res, err := OptimizeDatabase(t.TempDir())
	if err != nil {
		t.Fatalf("partial failure must not fail the whole call: %v", err)
	}
	t.Logf("EXPECTED: output names the failed steps\nACTUAL: %q", res.Output)
	if !strings.Contains(res.Output, "Transients") || !strings.Contains(res.Output, "failed") ||
		!strings.Contains(res.Output, "optimize") {
		t.Fatalf("PROBLEM CONFIRMED: failed steps are not reported: %q", res.Output)
	}
	if res.TransientsCleaned != 0 || res.TablesOptimized != 0 {
		t.Errorf("failed steps must not be counted: %+v", res)
	}
}

func TestOptimizeDatabaseReportsPartialDeletes(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	// Revision 2 cannot be deleted.
	execCommandFn = optimizeCmd("1 2 3", func(j string) bool {
		return strings.Contains(j, "post delete 2 ")
	})
	res, err := OptimizeDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: revisions deleted 2 of 3\nACTUAL: %q", res.Output)
	if res.RevisionsDeleted != 2 {
		t.Fatalf("RevisionsDeleted = %d, want 2", res.RevisionsDeleted)
	}
	if !strings.Contains(res.Output, "Revisions deleted: 2") || !strings.Contains(res.Output, "1 failed") {
		t.Fatalf("PROBLEM CONFIRMED: failed deletes are not reported: %q", res.Output)
	}
}

func TestOptimizeDatabaseHealthyRunIsUnchanged(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	execCommandFn = optimizeCmd("1 2 3", nil)
	res, err := OptimizeDatabase(t.TempDir())
	if err != nil {
		t.Fatalf("healthy run: %v", err)
	}
	if res.RevisionsDeleted != 3 || res.TransientsCleaned != 1 || res.TablesOptimized != 1 {
		t.Errorf("healthy run counters: %+v", res)
	}
	if strings.Contains(res.Output, "failed") {
		t.Errorf("healthy run must not mention failures: %q", res.Output)
	}
	if !strings.Contains(res.Output, "Revisions deleted: 3") || !strings.Contains(res.Output, "Tables optimized") {
		t.Errorf("healthy run output changed: %q", res.Output)
	}
}
