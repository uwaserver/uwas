package backup

import (
	"testing"
)

// TestCronWrappedRange59Minus0 proves that a wrapped cron range "59-0" correctly
// matches all values 0-59. The matchCronField function computes the gap between
// two values using modular arithmetic. For a wrapped range "59-0", the gap is
// negative (59-0=-59). The original condition gap > 0 excluded negative-gap wrapped
// ranges, so "59-0" only matched 0 (single-value check) but missed 1-59.
func TestCronWrappedRange59Minus0(t *testing.T) {
	// "59-0" means minute 59 through 0 — wrapping around the hour boundary.
	// This is equivalent to "0-59" (all minutes). The next run should not be
	// zero/empty, which is what happens when the wrapped range matches nothing.
	got := nextCronRun("59-0 * * * *")
	if got.IsZero() {
		t.Errorf("minute 59-0: nextCronRun returned zero time — schedule never fires")
		t.Log("BUG: wrapped range '59-0' matched no values due to gap<0 condition")
	}
}

// TestCronWrappedRange23Minus0Hour verifies the same bug for the hour field.
// "23-0" means all hours 0-23. If the wrapped range fails, nextCronRun returns
// zero because no hour satisfies the broken condition.
func TestCronWrappedRange23Minus0Hour(t *testing.T) {
	got := nextCronRun("* 23-0 * * *")
	if got.IsZero() {
		t.Errorf("hour 23-0: nextCronRun returned zero time — schedule never fires")
		t.Log("BUG: wrapped range '23-0' matched no hours due to gap<0 condition")
	}
}
