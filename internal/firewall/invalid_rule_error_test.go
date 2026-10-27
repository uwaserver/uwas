package firewall

import (
	"errors"
	"os/exec"
	"testing"
)

// F34000: a rule the request itself got wrong (bad port, protocol, source,
// action, direction, a protected port) was indistinguishable from an ufw
// failure, so the admin API answered 500 for what is a client error. The
// rejection now carries ErrInvalidRule; a genuine ufw failure does not.
func TestInvalidRuleRejectionsAreMarked(t *testing.T) {
	origLook, origCmd := execLookPathFn, execCommandFn
	defer func() { execLookPathFn, execCommandFn = origLook, origCmd }()
	execLookPathFn = func(string) (string, error) { return "/usr/sbin/ufw", nil }
	execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("true") }

	cases := []struct {
		name string
		call func() error
		want bool
	}{
		{"allow bad port", func() error { return AllowPortFrom("99999", "tcp", "") }, true},
		{"allow port word", func() error { return AllowPortFrom("web", "tcp", "") }, true},
		{"allow bad proto", func() error { return AllowPortFrom("8080", "icmp", "") }, true},
		{"allow bad source", func() error { return AllowPortFrom("8080", "tcp", "not-an-ip") }, true},
		{"deny bad port", func() error { return DenyPortFrom("0", "tcp", "") }, true},
		{"deny protected port", func() error { return DenyPortFrom("22", "tcp", "") }, true},
		{"move bad direction", func() error { return MoveRule(1, "sideways") }, true},
		{"move bad number", func() error { return MoveRule(0, "up") }, true},
		// Controls: valid input succeeds, and an ufw failure is not a client error.
		{"allow valid", func() error { return AllowPortFrom("8080", "tcp", "") }, false},
		{"ufw failure", func() error {
			execCommandFn = func(_ string, a ...string) *exec.Cmd {
				if len(a) > 0 && a[0] == "status" {
					return exec.Command("true")
				}
				return exec.Command("false")
			}
			defer func() { execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("true") } }()
			return AllowPortFrom("8081", "tcp", "")
		}, false},
	}
	for _, c := range cases {
		err := c.call()
		got := errors.Is(err, ErrInvalidRule)
		if got != c.want {
			t.Errorf("%s: errors.Is(ErrInvalidRule) = %v, want %v (err=%v)", c.name, got, c.want, err)
		}
		if c.want && err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
	}
}
