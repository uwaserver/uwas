package backup

import (
	"testing"
	"time"
)

// TestCronWeekday7Fires verifies that cron weekday=7 (Sunday alias) is handled
// correctly. Cron accepts 0=Sunday and 7=Sunday as equivalent. Before the fix,
// matchCronField compared the raw cron field integer directly against Go's
// time.Weekday (0=Sunday … 6=Saturday), so weekday=7 matched nothing and
// "0 0 * * 7" never fired.
func TestCronWeekday7Fires(t *testing.T) {
	now := time.Now()

	// Schedule for midnight every Sunday via weekday=0 (canonical Sunday)
	schedule0 := "0 0 * * 0"
	next0 := nextCronRun(schedule0)

	// Schedule for midnight every Sunday via weekday=7 (standard Sunday alias)
	schedule7 := "0 0 * * 7"
	next7 := nextCronRun(schedule7)

	if next0.IsZero() {
		t.Fatalf("BUG: weekday=0 (%q) produced zero time — schedule never fires", schedule0)
	}
	if next7.IsZero() {
		t.Fatalf("BUG: weekday=7 (%q) produced zero time — the 7=Sunday alias is broken", schedule7)
	}

	// Both represent the same schedule (midnight Sunday). They must produce
	// the same upcoming Sunday.
	if !next0.Equal(next7) {
		t.Errorf("weekday=0 and weekday=7 should produce the same next run:\n  weekday=0: %v\n  weekday=7: %v",
			next0.Format(time.RFC3339), next7.Format(time.RFC3339))
	}

	// Both must be in the future.
	if !next0.After(now) || !next7.After(now) {
		t.Errorf("next run must be after now (%v):\n  weekday=0: %v\n  weekday=7: %v",
			now.Format(time.RFC3339),
			next0.Format(time.RFC3339),
			next7.Format(time.RFC3339))
	}
}

// TestCronWeekdayMixed verifies that weekday lists containing 7 work correctly.
func TestCronWeekdayMixed(t *testing.T) {
	// Schedule for Saturday (6) and Sunday (7) — a common "weekend" schedule.
	schedule := "0 0 * * 6,7"
	next := nextCronRun(schedule)
	if next.IsZero() {
		t.Fatalf("BUG: weekday=6,7 (%q) produced zero time — weekday list with 7 is broken", schedule)
	}
	weekday := next.Weekday()
	if weekday != time.Saturday && weekday != time.Sunday {
		t.Errorf("next run for %q is %v (weekday=%d), expected Saturday (6) or Sunday (0)",
			schedule, next.Format(time.RFC3339), weekday)
	}
}
