package cache

// F32500: an ESI fragment's freshness lifetime was read from the first
// Cache-Control line only, and only from max-age. Directives are legitimately
// split across several header lines (F15300 fixed the page cache for that),
// and a shared cache must prefer s-maxage. A fragment declared fresh for 5 s on
// a second line was cached for the 60 s default, so visitors saw content up to
// 55 s older than the origin allows.

import (
	"net/http"
	"testing"
	"time"
)

func TestFragmentTTLMultiLineAndSMaxAge(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  time.Duration
	}{
		{"single line (control)", []string{"public, max-age=600"}, 600 * time.Second},
		{"max-age on second line", []string{"public", "max-age=5"}, 5 * time.Second},
		{"s-maxage wins for a shared cache", []string{"max-age=300, s-maxage=30"}, 30 * time.Second},
		{"quoted value", []string{`max-age="45"`}, 45 * time.Second},
		{"max-age=0 stays unset (control)", []string{"max-age=0"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			for _, l := range c.lines {
				h.Add("Cache-Control", l)
			}
			got := parseFragmentTTL(h)
			t.Logf("EXPECTED: %s\nACTUAL: %s", c.want, got)
			if got != c.want {
				t.Fatalf("PROBLEM CONFIRMED: ttl = %s, want %s", got, c.want)
			}
		})
	}
}
