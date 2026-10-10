package backup

import "testing"

// A step on a range ("0-30/10") or on a start value ("5/15") must select every
// step-th value. parseInt skips non-digits, so the '/' used to be dropped:
// "0-30/10" became the range 0-3010 (every minute) and "5/15" became 515
// (never) — a nightly-window backup schedule turned into a backup every
// minute (F1960).
func TestMatchCronFieldRangeAndStartSteps(t *testing.T) {
	rng := func(lo, hi int) []int {
		var s []int
		for i := lo; i <= hi; i++ {
			s = append(s, i)
		}
		return s
	}
	cases := []struct {
		field string
		want  []int
	}{
		{"0-30/10", []int{0, 10, 20, 30}},
		{"10-50/20", []int{10, 30, 50}},
		{"5/15", []int{5, 20, 35, 50}},
		{"1-5/2", []int{1, 3, 5}},
		{"0-59/30", []int{0, 30}},
		{"0-30/10,45", []int{0, 10, 20, 30, 45}},
		{"2-8/1", rng(2, 8)},
		{"7-7/3", []int{7}},
		// A zero or missing step, or a reversed range, selects nothing rather
		// than everything.
		{"0-30/0", nil},
		{"5/", nil},
		{"30-0/5", nil},
		// Forms that already worked keep working.
		{"*/15", []int{0, 15, 30, 45}},
		{"0-30", rng(0, 30)},
		{"5", []int{5}},
		{"1-5,30", []int{1, 2, 3, 4, 5, 30}},
		{"59-0", append([]int{0}, 59)},
	}
	for _, c := range cases {
		want := map[int]bool{}
		for _, v := range c.want {
			want[v] = true
		}
		for v := 0; v < 60; v++ {
			if got := matchCronField(v, c.field); got != want[v] {
				t.Errorf("matchCronField(%d, %q) = %v, want %v", v, c.field, got, want[v])
			}
		}
	}
}
