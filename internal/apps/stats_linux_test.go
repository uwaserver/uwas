//go:build linux

package apps

import "testing"

func TestParseHumanSize(t *testing.T) {
	const (
		kib int64 = 1024
		mib       = kib * 1024
		gib       = mib * 1024
		tib       = gib * 1024
	)
	tibFloat := float64(tib)

	cases := []struct {
		in   string
		want int64
	}{
		{"500B", 500},
		{"1KiB", kib},
		{"1.5MiB", int64(1.5 * float64(mib))},
		{"2GiB", 2 * gib},
		{"1.2TiB", int64(1.2 * tibFloat)},
		{"500MB", 500 * 1000 * 1000},
		{"", 0},
		{"garbage", 0},
		{"42", 42}, // no suffix → raw bytes
	}
	for _, c := range cases {
		got := parseHumanSize(c.in)
		diff := got - c.want
		if diff < 0 {
			diff = -diff
		}
		if diff > 2 {
			t.Errorf("parseHumanSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
