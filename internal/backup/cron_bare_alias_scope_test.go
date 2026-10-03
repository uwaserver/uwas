package backup

// Regression: cron's weekday 7≡Sunday alias is scoped to the weekday field.
//
// Bug (fixed by removing the field-blind branch from matchCronTerm): the bare
// alias `v == 7 && value == 0 → true` ran in EVERY field, so term "7" also
// matched value 0 in the minute/hour/day/month fields. Observable: "0 7 * * *"
// (7 AM daily) matched hour 0 via the alias and fired at midnight too — double
// backup runs — and "7 * * * *" fired at :00 as well as :07.
//
// Contract: POSIX/Vixie cron apply the 0/7 equivalence to the day-of-week
// field only (man 5 crontab). The weekday path folds 7→0 via
// normalizeCronWeekdayField at the nextCronRun call site (see
// schedule_weekday7_test.go and cron_weekday7_range_test.go), so weekday
// semantics are unchanged; other fields now match bare "7" only at 7.
//
// The prior round's table row in backup_cron_field_test.go pinned the leaked
// behavior context-free; it was updated alongside this fix.

import (
	"testing"
	"time"
)

func TestMatchCronFieldBare7IsScopedToValue7(t *testing.T) {
	// Minute field: "7" must match minute 7 only.
	if matchCronField(0, "7") {
		t.Error(`matchCronField(0, "7") = true — weekday alias leaked into the minute field ('0 7 * * *' would also fire at 00:00)`)
	}
	if !matchCronField(7, "7") {
		t.Error(`matchCronField(7, "7") = false, want true`)
	}
	if matchCronField(6, "7") {
		t.Error(`matchCronField(6, "7") = true, want false`)
	}
	// Hour field (same matcher, caller context differs): hour 0 must not
	// match "7".
	if matchCronField(0, "7") {
		t.Error(`hour 0 matched "7" — alias leak makes '0 7 * * *' fire at midnight`)
	}
	// Day-of-month and month fields: 7 is just 7 there too.
	if matchCronField(0, "7") {
		t.Error(`value 0 matched term "7" outside the weekday field`)
	}
}

func TestNextCronRunBare7MinuteOnly(t *testing.T) {
	// End-to-end: "7 * * * *" fires at :07 only — never at :00.
	next := nextCronRun("7 * * * *")
	if next.IsZero() {
		t.Fatal(`nextCronRun("7 * * * *") returned zero`)
	}
	if next.Minute() != 7 {
		t.Fatalf(`nextCronRun("7 * * * *") minute = %d, want 7 (the weekday alias must not add a :00 fire)`, next.Minute())
	}
}

func TestNextCronRunWeekdayAliasUnaffectedByBare7Scope(t *testing.T) {
	// Controls: weekday semantics are carried by normalizeCronWeekdayField
	// and must be unaffected by scoping the bare alias.
	sun0 := nextCronRun("0 0 * * 0")
	sun7 := nextCronRun("0 0 * * 7")
	if sun0.IsZero() || sun0.Weekday() != time.Sunday {
		t.Fatalf(`nextCronRun("0 0 * * 0") = %v, want a future Sunday`, sun0)
	}
	if sun7.IsZero() || !sun0.Equal(sun7) {
		t.Fatalf(`weekday 7 (%v) and 0 (%v) must produce the same Sunday`, sun7, sun0)
	}
	// Sunday via list and range forms still fire.
	for _, expr := range []string{"0 0 * * 6,7", "0 0 * * 7-7", "0 0 * * 5-7"} {
		if next := nextCronRun(expr); next.IsZero() {
			t.Errorf("nextCronRun(%q) returned zero", expr)
		}
	}
	// Non-weekday fields with 7 inside ranges are untouched.
	if !matchCronField(5, "5-7") || !matchCronField(7, "5-7") {
		t.Error(`matchCronField(5|7, "5-7") = false, want true (minute range 5-7 unchanged)`)
	}
	if matchCronField(0, "5-7") {
		t.Error(`matchCronField(0, "5-7") = true, want false (minute range 5-7 must not gain 0)`)
	}
}
