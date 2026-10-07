//go:build linux

package terminal

// Regression guard for the terminal window-size path.
//
// setWinSize took cols/rows straight from the client — they are decoded out of a
// WebSocket frame into resizeMsg (pty_linux.go) — and converted them to uint16
// before any range check. The conversion reinterpreted the number rather than
// applying it:
//
//	cols = 65536  ->  uint16 = 0      -> a 0-column window
//	cols = -1     ->  uint16 = 65535  -> a 65535-column window
//	rows = 0      ->  uint16 = 0      -> a 0-row window
//
// The kernel accepts a 0x0 TIOCSWINSZ, so nothing errored: the user's shell was
// left rendering against a zero-size window with no feedback. Out-of-range
// resizes are now rejected, which leaves the last good size in place.
//
// These tests read the size back through TIOCGWINSZ on the slave, so they
// observe what the kernel actually applied rather than what was requested.

import (
	"os"
	"testing"
)

// applyAndRead sets a size then reads it back from the slave.
func applyAndRead(t *testing.T, master, slave *os.File, cols, rows int) (int, int) {
	t.Helper()
	setWinSize(master, cols, rows)
	gotCols, gotRows, err := getWinSize(slave)
	if err != nil {
		t.Fatalf("getWinSize: %v", err)
	}
	return gotCols, gotRows
}

// TestOutOfRangeWinSizeIsRejected is the regression guard. Each case starts
// from a known-good 80x24 baseline; a rejected resize must leave it untouched.
func TestOutOfRangeWinSizeIsRejected(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("no PTY available: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	cases := []struct {
		name       string
		cols, rows int
	}{
		{"cols wraps to zero", 65536, 24},
		{"rows wraps to zero", 80, 65536},
		{"rows zero", 80, 0},
		{"cols zero", 0, 24},
		{"both zero", 0, 0},
		{"negative cols", -1, 24},
		{"negative rows", 80, -1},
		{"both wrap to zero", 65536, 65536},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if c, r := applyAndRead(t, master, slave, 80, 24); c != 80 || r != 24 {
				t.Fatalf("baseline not applied: %dx%d, want 80x24", c, r)
			}

			gotCols, gotRows := applyAndRead(t, master, slave, tc.cols, tc.rows)

			if gotCols != 80 || gotRows != 24 {
				t.Fatalf("resize(%d,%d) changed the window to %dx%d; an out-of-range "+
					"size must be rejected and leave the last good 80x24 in place",
					tc.cols, tc.rows, gotCols, gotRows)
			}
		})
	}
}

// TestInRangeWinSizeStillApplies is the control: ordinary resize values must
// keep working. A "fix" that rejected everything would pass the test above and
// silently break every real resize.
func TestInRangeWinSizeStillApplies(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("no PTY available: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	for _, tc := range []struct{ cols, rows int }{
		{132, 50}, // the value the pre-existing TestSetWinSize uses
		{80, 24},
		{1, 1}, // smallest usable window
		{200, 60},
		{65535, 65535}, // largest representable
	} {
		gotCols, gotRows := applyAndRead(t, master, slave, tc.cols, tc.rows)
		if gotCols != tc.cols || gotRows != tc.rows {
			t.Fatalf("resize(%d,%d) applied as %dx%d, want %dx%d",
				tc.cols, tc.rows, gotCols, gotRows, tc.cols, tc.rows)
		}
	}
}

// TestSetWinSizeSwallowsIoctlErrorOnClosedFd pins the documented best-effort
// behaviour that the range check must not turn into a panic: the ioctl error is
// deliberately ignored.
func TestSetWinSizeSwallowsIoctlErrorOnClosedFd(t *testing.T) {
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("no PTY available: %v", err)
	}
	slave.Close()
	master.Close()
	setWinSize(master, 80, 24) // closed fd, valid range: must not panic
}
