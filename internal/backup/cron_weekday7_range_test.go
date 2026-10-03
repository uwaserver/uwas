package backup

// Regression: cron weekday ranges containing the 7=Sunday alias must include
// Sunday.
//
// Bug (fixed by normalizing the weekday field in nextCronRun): the 7≡Sunday
// alias existed only in matchCronTerm's bare-value branch, so a range compared
// raw integers and weekday candidates are always 0..6:
//
//	"0 0 * * 7-7"  matched nothing → nextCronRun returned zero → the
//	               schedule never fired at all (ScheduleBackupCron then
//	               reports itself inactive).
//	"0 0 * * 1-7"  matched 1..6, silently skipping every Sunday even though
//	               Mon-Sun is the natural "every day" spelling.
//	"0 0 * * 5-7"  skipped Sunday from a Fri-Sun weekend schedule.
//
// Contract: POSIX/Vixie cron treats weekday 0 and 7 as the same day including
// inside ranges (Vixie folds dow bit 7 into bit 0 after range expansion), and
// matchCronTerm's own bare-value branch already documents that alias intent.
// The fix folds 7→0 on weekday-field terms at the nextCronRun call site, so
// "1-7" becomes the wrapped range "1-0" ({1..6, 0}); minute/hour/day/month
// fields are untouched (their 7 is just 7).

import (
	"testing"
	"time"
)

func TestNextCronRunWeekday7Range7To7Fires(t *testing.T) {
	next := nextCronRun("0 0 * * 7-7")
	if next.IsZero() {
		t.Fatal(`nextCronRun("0 0 * * 7-7") returned zero — a Sunday schedule written with the 7=Sunday alias never fires`)
	}
	if next.Weekday() != time.Sunday {
		t.Fatalf("nextCronRun(\"0 0 * * 7-7\") = %v (%v), want a Sunday", next.Format(time.RFC3339), next.Weekday())
	}
}

func TestNextCronRunWeekday7RangesIncludeSunday(t *testing.T) {
	cases := []struct {
		expr  string
		label string
	}{
		{"0 0 * * 1-7", "Mon-Sun (every day)"},
		{"0 0 * * 5-7", "Fri-Sun (weekend)"},
		{"0 0 * * 6-7", "Sat-Sun"},
		{"0 0 * * 0-7", "0-7 (all days, both Sunday spellings)"},
		{"0 0 * * 7-0", "wrapped 7-0 (Sun..Sun)"},
	}
	for _, tc := range cases {
		next := nextCronRun(tc.expr)
		if next.IsZero() {
			t.Errorf("nextCronRun(%q) [%s] returned zero — Sunday dropped from the weekday range", tc.expr, tc.label)
		}
	}
}

func TestMatchCronFieldWeekday7FoldedForms(t *testing.T) {
	// The folded forms the normalizer produces must match through the existing
	// range machinery: "1-0" wraps ({1..6, 0}), "0-0" is Sunday only.
	if !matchCronField(0, "1-0") {
		t.Error(`matchCronField(0, "1-0") = false, want true (Sunday inside wrapped 1-0)`)
	}
	if !matchCronField(3, "1-0") {
		t.Error(`matchCronField(3, "1-0") = false, want true (Wednesday inside wrapped 1-0)`)
	}
	if !matchCronField(0, "0-0") {
		t.Error(`matchCronField(0, "0-0") = false, want true`)
	}
	if matchCronField(3, "0-0") {
		t.Error(`matchCronField(3, "0-0") = true, want false (0-0 is Sunday only)`)
	}
}

func TestNextCronRunWeekdayNormalizationControls(t *testing.T) {
	// Control: canonical Sunday (0) keeps working.
	sun0 := nextCronRun("0 0 * * 0")
	if sun0.IsZero() || sun0.Weekday() != time.Sunday {
		t.Fatalf("nextCronRun(\"0 0 * * 0\") = %v, want a future Sunday", sun0)
	}
	// Control: bare alias (7) equals canonical 0 — pinned since the weekday-7 fix.
	if sun7 := nextCronRun("0 0 * * 7"); sun7.IsZero() || !sun0.Equal(sun7) {
		t.Fatalf("weekday 7 (%v) and 0 (%v) must produce the same Sunday", sun7, sun0)
	}
	// Control: lists with 7 still work (prior round's weekend schedule).
	if next := nextCronRun("0 0 * * 6,7"); next.IsZero() {
		t.Fatal(`nextCronRun("0 0 * * 6,7") returned zero`)
	}
	// Control: non-weekday fields are untouched — a minute "59-0" wrap and a
	// minute list containing ranges behave exactly as before the fix.
	if got := nextCronRun("59-0 * * * *"); got.IsZero() {
		t.Fatal(`nextCronRun("59-0 * * * *") returned zero`)
	}
	if !matchCronField(6, "5-7") || !matchCronField(5, "5-7") {
		t.Error(`matchCronField(5|6, "5-7") = false, want true (range endpoints below 7 unchanged)`)
	}
}
