package firewall

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// fakeIPTables is a stateful stand-in for iptables/ip6tables: -I adds a rule,
// -D removes ONE matching rule and fails like the real tool ("Bad rule (does
// not exist).", exit 1) when none is left.
type fakeIPTables struct {
	rules map[string]int // "bin|args after the verb" -> number of identical rules
}

func installFakeIPTables(t *testing.T) *fakeIPTables {
	t.Helper()
	f := &fakeIPTables{rules: map[string]int{}}

	origCmd, origPath, origOS := execCommandFn, execLookPathFn, runtimeGOOS
	t.Cleanup(func() { execCommandFn, execLookPathFn, runtimeGOOS = origCmd, origPath, origOS })

	runtimeGOOS = "linux"
	execLookPathFn = func(file string) (string, error) {
		if file == "iptables" || file == "ip6tables" {
			return "/usr/sbin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		if len(arg) < 2 {
			return exec.Command("true")
		}
		verb := arg[0]
		// "-I INPUT 1 -s IP -j DROP" and "-D INPUT -s IP -j DROP" name the same rule.
		rest := arg[1:]
		if verb == "-I" && len(rest) > 1 {
			rest = append([]string{rest[0]}, rest[2:]...)
		}
		key := name + "|" + strings.Join(rest, " ")
		switch verb {
		case "-I":
			f.rules[key]++
		case "-D":
			if f.rules[key] == 0 {
				return exec.Command("sh", "-c", "echo '"+name+": Bad rule (does not exist).'; exit 1")
			}
			f.rules[key]--
		}
		return exec.Command("true")
	}
	return f
}

func (f *fakeIPTables) remaining() int {
	n := 0
	for _, c := range f.rules {
		n += c
	}
	return n
}

// A block that was applied twice (a manual block over an existing autoblock, a
// replayed queue entry) left a second DROP rule behind after one unblock: the
// address stayed cut off at the kernel although the blocker had forgotten it.
func TestUnblockIPRemovesDuplicateIPTablesRules(t *testing.T) {
	for _, ip := range []string{"203.0.113.9", "2001:db8::9"} {
		t.Run(ip, func(t *testing.T) {
			f := installFakeIPTables(t)

			// Control: one block, one unblock leaves nothing.
			if err := BlockIP(ip, ""); err != nil {
				t.Fatalf("BlockIP: %v", err)
			}
			if err := UnblockIP(ip); err != nil {
				t.Fatalf("UnblockIP: %v", err)
			}
			if got := f.remaining(); got != 0 {
				t.Fatalf("control: %d rules left after block+unblock", got)
			}

			for i := 0; i < 3; i++ {
				if err := BlockIP(ip, ""); err != nil {
					t.Fatalf("BlockIP #%d: %v", i, err)
				}
			}
			if err := UnblockIP(ip); err != nil {
				t.Fatalf("UnblockIP: %v", err)
			}
			if got := f.remaining(); got != 0 {
				t.Errorf("EXPECTED: 0 rules left after unblock; ACTUAL: %d", got)
			}
		})
	}
}

// UnblockIP documents that a missing rule is not an error (expiry may fire after
// the operator cleared the rule); the ufw branch honours that, iptables did not.
func TestUnblockIPMissingIPTablesRuleIsNotAnError(t *testing.T) {
	installFakeIPTables(t)
	if err := UnblockIP("203.0.113.10"); err != nil {
		t.Errorf("EXPECTED: nil for an absent rule; ACTUAL: %v", err)
	}
}

// A failure other than "rule absent" must still surface.
func TestUnblockIPIPTablesRealFailureStillReported(t *testing.T) {
	installFakeIPTables(t)
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("sh", "-c", fmt.Sprintf("echo '%s: Permission denied (you must be root)'; exit 4", name))
	}
	if err := UnblockIP("203.0.113.11"); err == nil {
		t.Error("a permission failure must be reported")
	}
}
