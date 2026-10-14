package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A client's Cache-Control / Pragma no-cache must never bypass the cache, in
// any spelling: honouring it let a flood that set the header skip the cache
// and land every request on the origin.
func TestShouldBypassIgnoresClientNoCache(t *testing.T) {
	mk := func(hdr ...string) *http.Request {
		r := httptest.NewRequest("GET", "/a", nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Add(hdr[i], hdr[i+1])
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"lower", mk("Cache-Control", "no-cache"), false},
		{"mixed case", mk("Cache-Control", "No-Cache"), false},
		{"in list upper", mk("Cache-Control", "max-age=0, NO-CACHE"), false},
		{"split lines", mk("Cache-Control", "max-age=0", "Cache-Control", "no-cache"), false},
		{"with argument", mk("Cache-Control", `no-cache="Set-Cookie"`), false},
		{"pragma lower", mk("Pragma", "no-cache"), false},
		{"pragma mixed", mk("Pragma", "No-Cache"), false},
		{"max-age only", mk("Cache-Control", "max-age=60"), false},
		{"longer token", mk("Cache-Control", "no-cache-ext"), false},
		{"no headers", mk(), false},
		{"empty value", mk("Cache-Control", ""), false},
	}
	for _, c := range cases {
		if got := ShouldBypass(c.r); got != c.want {
			t.Errorf("%s: ShouldBypass=%v want %v", c.name, got, c.want)
		}
	}
}
