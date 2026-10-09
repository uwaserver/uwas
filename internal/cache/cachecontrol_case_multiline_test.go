package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Cache-Control directives are case-insensitive and may be split across
// header lines; a private or no-store response must never be shared.
func TestIsCacheableRefusesPrivateAnyCaseOrLine(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/a", nil)
	for _, vals := range [][]string{
		{"Private"},
		{"NO-STORE"},
		{"public, max-age=60", "private"},
		{"max-age=60", "no-store"},
	} {
		h := http.Header{}
		for _, v := range vals {
			h.Add("Cache-Control", v)
		}
		if IsCacheable(r, http.StatusOK, h) {
			t.Errorf("Cache-Control %q was cacheable", vals)
		}
	}
	if !IsCacheable(r, http.StatusOK, http.Header{"Cache-Control": {"Public, Max-Age=60"}}) {
		t.Error("public response not cacheable")
	}
}
