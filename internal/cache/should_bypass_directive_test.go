package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Request Cache-Control / Pragma directive names are case-insensitive and
// the list may span several header lines (F1391).
func TestShouldBypassDirectiveTokens(t *testing.T) {
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
		{"lower", mk("Cache-Control", "no-cache"), true},
		{"mixed case", mk("Cache-Control", "No-Cache"), true},
		{"in list upper", mk("Cache-Control", "max-age=0, NO-CACHE"), true},
		{"split lines", mk("Cache-Control", "max-age=0", "Cache-Control", "no-cache"), true},
		{"with argument", mk("Cache-Control", `no-cache="Set-Cookie"`), true},
		{"pragma lower", mk("Pragma", "no-cache"), true},
		{"pragma mixed", mk("Pragma", "No-Cache"), true},
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
