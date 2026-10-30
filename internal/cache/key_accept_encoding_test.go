package cache

import (
	"net/http/httptest"
	"testing"
)

func TestAcceptEncodingClass(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"identity":              "",
		"gzip":                  "gzip",
		"GZIP, deflate":         "gzip",
		"gzip, br":              "br",
		"br;q=0, gzip":          "gzip",
		"gzip;q=0, br;q=0":      "",
		"gzip, deflate, junk42": "gzip",
		"deflate, zstd":         "",
		"br;q=0.5, gzip;Q=1":    "br",
		"gzip;q=notanumber":     "gzip",
	} {
		if got := acceptEncodingClass(in); got != want {
			t.Errorf("acceptEncodingClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeyIgnoresAcceptEncodingSpelling(t *testing.T) {
	key := func(ae string) string {
		r := httptest.NewRequest("GET", "http://h/p", nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		return GenerateKey(r, []string{"Accept-Encoding", "X-Variant"})
	}
	if key("gzip") != key("gzip, deflate, x1") {
		t.Error("spellings of the same coding produced different keys")
	}
	if key("gzip") == key("br") || key("gzip") == key("") {
		t.Error("distinct codings must keep distinct keys")
	}
	// A non-encoding vary header still keys on its raw value.
	a := httptest.NewRequest("GET", "http://h/p", nil)
	a.Header.Set("X-Variant", "one")
	b := httptest.NewRequest("GET", "http://h/p", nil)
	b.Header.Set("X-Variant", "two")
	if GenerateKey(a, []string{"X-Variant"}) == GenerateKey(b, []string{"X-Variant"}) {
		t.Error("X-Variant values collapsed")
	}
}
