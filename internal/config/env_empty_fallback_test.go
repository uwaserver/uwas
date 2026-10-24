package config

import "testing"

// POSIX/compose `${VAR:-x}` falls back when VAR is unset OR empty; only the
// colon-less form `${VAR-x}` is unset-only. UWAS documents the `:-` form.
func TestExpandEnvVarsEmptyUsesFallback(t *testing.T) {
	t.Setenv("R152_EMPTY", "")
	t.Setenv("R152_SET", "real")
	cases := map[string]string{
		"${R152_EMPTY:-fb}":   "fb",   // set but empty → fallback
		"${R152_SET:-fb}":     "real", // control
		"${R152_UNSET_X:-fb}": "fb",   // control
		"${R152_EMPTY:-}":     "",
		"a=${R152_EMPTY}":     "a=", // plain form keeps empty value
	}
	for in, want := range cases {
		if got := expandEnvVars(in); got != want {
			t.Errorf("expandEnvVars(%q) = %q, want %q", in, got, want)
		}
	}
}
