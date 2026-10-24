package cache

import (
	"net/http"
	"testing"
	"time"
)

// F25400: Cache-Control may be split across several header lines; the origin's
// freshness lifetime on a later line must still cap the TTL, and an absurd
// max-age must not wrap around Duration into a tiny positive cap.
func TestCapTTLMultiLineAndOverflow(t *testing.T) {
	hour := time.Hour
	cases := []struct {
		name  string
		lines []string
		want  time.Duration
	}{
		{"single line control", []string{"max-age=60"}, time.Minute},
		{"max-age on second line", []string{"public", "max-age=60"}, time.Minute},
		{"s-maxage on second line wins", []string{"max-age=120", "s-maxage=30"}, 30 * time.Second},
		{"overflowing max-age ignored", []string{"max-age=9223372037"}, hour},
		{"huge max-age ignored", []string{"max-age=99999999999999999999"}, hour},
	}
	for _, c := range cases {
		h := http.Header{}
		for _, l := range c.lines {
			h.Add("Cache-Control", l)
		}
		if got := CapTTL(hour, h); got != c.want {
			t.Errorf("%s: EXPECTED %v ACTUAL %v", c.name, c.want, got)
		}
	}
}
