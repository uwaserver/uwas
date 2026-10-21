package phpmanager

import (
	"strings"
	"testing"
)

func TestAdvisory(t *testing.T) {
	cases := []struct {
		v         string
		supported bool
		warn      string // substring, "" means none
	}{
		{"8.5.12", true, ""},
		{"8.5.11", true, "update to 8.5.12"},
		{"8.4.27", true, ""},
		{"8.4.26", true, "update to 8.4.27"},
		{"8.3.36", true, ""},
		{"8.3.32", true, "update to 8.3.36"},
		{"8.2.35", true, ""},
		{"8.2.34", true, "update to 8.2.35"},
		{"8.1.40", false, "end-of-life"},
		{"7.4.33", false, "end-of-life"},
		{"8.6.0", true, ""},
		{"8.3.36-dev", true, ""},
		{"garbage", true, ""},
	}
	for _, c := range cases {
		sup, warn := Advisory(c.v)
		if sup != c.supported {
			t.Errorf("%s: supported=%v want %v", c.v, sup, c.supported)
		}
		if (c.warn == "") != (warn == "") || !strings.Contains(warn, c.warn) {
			t.Errorf("%s: warning %q, want containing %q", c.v, warn, c.warn)
		}
	}
}

func TestSupportedBranch(t *testing.T) {
	for v, want := range map[string]bool{
		"8.2": true, "8.3.36": true, "8.5": true, "9.0": true,
		"8.1": false, "8.0": false, "7.4": false, "": false, "x.y": false, "8": false,
	} {
		if got := SupportedBranch(v); got != want {
			t.Errorf("SupportedBranch(%q)=%v want %v", v, got, want)
		}
	}
}

func TestRunInstallRejectsEOL(t *testing.T) {
	if _, err := RunInstall("8.1"); err == nil {
		t.Fatal("RunInstall(8.1) must fail")
	}
}
