package config

import "testing"

func TestCanonicalHostTrimsAbsoluteDotBeforePort(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM.:443":    "example.com:443",
		"*.Example.COM.:8443": "*.example.com:8443",
		"bücher.example.:443": "xn--bcher-kva.example:443",
		"[::1]:443":           "[::1]:443",
	} {
		if got := CanonicalHost(in); got != want {
			t.Errorf("CanonicalHost(%q) = %q, want %q", in, got, want)
		}
	}
}
