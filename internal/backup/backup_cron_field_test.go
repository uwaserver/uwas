package backup

import (
	"testing"
	"time"
)

// A cron field may mix ranges and single values ("1-5,30"). matchCronField used
// to test for a range before it tested for a list, so "1-5,30" entered the range
// branch, split on "-" into ["1", "5,30"], and parseInt silently dropped the
// comma — turning the field into the range 1-530, which matches every minute.
// The operator asked for a few backup windows and got all of them.
func TestMatchCronFieldListContainingRange(t *testing.T) {
	for _, v := range []int{1, 2, 3, 4, 5, 30} {
		if !matchCronField(v, "1-5,30") {
			t.Errorf("minute %d is in {1,2,3,4,5,30} but did not match %q", v, "1-5,30")
		}
	}
	for _, v := range []int{0, 6, 15, 29, 31, 45, 59} {
		if matchCronField(v, "1-5,30") {
			t.Errorf("minute %d is not in {1,2,3,4,5,30} but matched %q — the "+
				"list was re-parsed as a range", v, "1-5,30")
		}
	}
}

// TestMatchCronFieldListOfRanges covers several ranges in one field, the other
// shape the comma-first split has to handle.
func TestMatchCronFieldListOfRanges(t *testing.T) {
	field := "1-5,20-25,30"
	for _, v := range []int{1, 5, 20, 25, 30} {
		if !matchCronField(v, field) {
			t.Errorf("minute %d should match %q", v, field)
		}
	}
	for _, v := range []int{0, 6, 19, 26, 29, 31, 45} {
		if matchCronField(v, field) {
			t.Errorf("minute %d should not match %q", v, field)
		}
	}
}

// TestMatchCronFieldListMixingRangeStepAndValue pins the mixed term handling.
func TestMatchCronFieldListMixingRangeStepAndValue(t *testing.T) {
	field := "1-3,*/10,45"
	for _, v := range []int{1, 2, 3, 0, 10, 20, 30, 40, 50, 45} {
		if !matchCronField(v, field) {
			t.Errorf("value %d should match %q", v, field)
		}
	}
	for _, v := range []int{4, 5, 15, 46} {
		if matchCronField(v, field) {
			t.Errorf("value %d should not match %q", v, field)
		}
	}
}

// TestMatchCronFieldHourListContainingRange is the operator-facing form: a
// "work hours plus one evening slot" schedule must not widen to all 24 hours.
func TestMatchCronFieldHourListContainingRange(t *testing.T) {
	field := "9-17,20"
	for _, h := range []int{9, 17, 20} {
		if !matchCronField(h, field) {
			t.Errorf("hour %d should match %q", h, field)
		}
	}
	for _, h := range []int{0, 8, 18, 19, 21, 23} {
		if matchCronField(h, field) {
			t.Errorf("hour %d should not match %q", h, field)
		}
	}
}

// TestNextCronRunListContainingRange drives the same field through the
// production entry point the server calls (server.go:977 ->
// ScheduleBackupCron -> nextCronRun), so the schedule the operator configures is
// what the scheduler actually produces.
func TestNextCronRunListContainingRange(t *testing.T) {
	next := nextCronRun("0 1-5,30 * * *")
	if next.IsZero() {
		t.Fatal(`nextCronRun("0 1-5,30 * * *") returned zero: schedule never fires`)
	}
	if next.Minute() != 0 {
		t.Fatalf("expected minute 0, got %d", next.Minute())
	}
	if h := next.Hour(); !(h >= 1 && h <= 5) && h != 0 {
		t.Fatalf("next run at %02d:%02d is outside the configured 01:00-05:00 "+
			"and 00:30 windows", h, next.Minute())
	}
}

// --- Controls: the pre-existing single-term forms must be unaffected ---

func TestMatchCronFieldSingleTermFormsUnaffected(t *testing.T) {
	cases := []struct {
		field  string
		match  []int
		reject []int
	}{
		{"*", []int{0, 1, 30, 59}, nil},
		{"1,3,5", []int{1, 3, 5}, []int{0, 2, 4, 6, 30}},
		{"1-5", []int{1, 3, 5}, []int{0, 6, 30, 59}},
		{"*/15", []int{0, 15, 30, 45}, []int{1, 7, 14, 20}},
		{"7", []int{7}, []int{0, 1, 6}}, // bare 7 matches only 7 outside the weekday field; the dow 7≡Sunday alias is folded by normalizeCronWeekdayField at the weekday call site (schedule_weekday7_test.go)
	}
	for _, tc := range cases {
		for _, v := range tc.match {
			if !matchCronField(v, tc.field) {
				t.Errorf("%q should match %d", tc.field, v)
			}
		}
		for _, v := range tc.reject {
			if matchCronField(v, tc.field) {
				t.Errorf("%q should not match %d", tc.field, v)
			}
		}
	}
}

// TestNextCronRunPlainFieldsStillFire guards the field-by-field coverage a
// prior round added, so the comma-first split did not regress plain schedules.
func TestNextCronRunPlainFieldsStillFire(t *testing.T) {
	for _, expr := range []string{
		"* * * * *",
		"0 3 * * *",
		"*/10 * * * *",
		"0 1-5 * * *",
		"0 9-17 * * 1-5",
		"0 0 * * 1,3,5",
	} {
		if got := nextCronRun(expr); got.IsZero() {
			t.Errorf("nextCronRun(%q) returned zero", expr)
		} else if !got.After(time.Now().Add(-time.Minute)) {
			t.Errorf("nextCronRun(%q) = %v is not in the future", expr, got)
		}
	}
}
