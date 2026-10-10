package firewall

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// scopeFake serves a fixed `ufw status numbered` table, removes rows on
// `--force delete N` and records every other command. Never runs real ufw.
type scopeFake struct {
	mu   sync.Mutex
	rows []string
	cmds []string
}

func installScopeFake(t *testing.T, rows ...string) *scopeFake {
	t.Helper()
	f := &scopeFake{rows: rows}
	origCmd, origPath, origOS := execCommandFn, execLookPathFn, runtimeGOOS
	t.Cleanup(func() { execCommandFn, execLookPathFn, runtimeGOOS = origCmd, origPath, origOS })
	runtimeGOOS = "linux"
	execLookPathFn = func(string) (string, error) { return "/usr/sbin/ufw", nil }
	execCommandFn = func(_ string, arg ...string) *exec.Cmd {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(arg) >= 1 && arg[0] == "status" {
			var b strings.Builder
			b.WriteString("Status: active\n\n")
			for i, r := range f.rows {
				fmt.Fprintf(&b, "[%2d] %s\n", i+1, r)
			}
			return exec.Command("printf", "%s", b.String())
		}
		f.cmds = append(f.cmds, strings.Join(arg, " "))
		if len(arg) == 3 && arg[0] == "--force" && arg[1] == "delete" {
			var n int
			fmt.Sscanf(arg[2], "%d", &n)
			if n >= 1 && n <= len(f.rows) {
				f.rows = append(f.rows[:n-1], f.rows[n:]...)
			}
		}
		return exec.Command("true")
	}
	return f
}

// F755: app profiles, interfaces and destinations are part of a rule's
// identity; deduplication must not delete "OpenSSH" because "Nginx Full" has
// the same action/port/from, or a destination-scoped allow because a broader
// source allow exists.
func TestDeduplicateKeepsDistinctScopedRules(t *testing.T) {
	cases := map[string][]string{
		"app profiles": {
			"Nginx Full                 ALLOW IN    Anywhere",
			"OpenSSH                    ALLOW IN    Anywhere",
			"Nginx Full (v6)            ALLOW IN    Anywhere (v6)",
			"OpenSSH (v6)               ALLOW IN    Anywhere (v6)",
		},
		"destination vs source": {
			"10.0.0.5 3306/tcp          ALLOW IN    10.0.0.0/8",
			"Anywhere                   ALLOW IN    10.0.0.0/8",
		},
		"interface vs global deny": {
			"3306/tcp on eth0           DENY IN     Anywhere",
			"3306/tcp                   DENY IN     Anywhere",
		},
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			f := installScopeFake(t, rows...)
			DeduplicateRules()
			if len(f.cmds) != 0 {
				t.Fatalf("distinct rules were deleted: %q", f.cmds)
			}
		})
	}
	t.Run("genuine duplicate still removed", func(t *testing.T) {
		f := installScopeFake(t,
			"OpenSSH                    ALLOW IN    Anywhere",
			"OpenSSH                    ALLOW IN    Anywhere")
		DeduplicateRules()
		if len(f.cmds) != 1 || f.cmds[0] != "--force delete 2" {
			t.Fatalf("cmds = %q, want one delete of #2", f.cmds)
		}
	})
}

// F756: MoveRule can only recreate action/port/proto/from, so it must refuse a
// scoped rule before deleting it instead of re-adding it unscoped.
func TestMoveRuleRefusesScopedRule(t *testing.T) {
	for _, row := range []string{
		"3306/tcp on eth1           ALLOW IN    Anywhere",
		"10.0.0.5 3306/tcp          ALLOW IN    10.0.0.0/8",
	} {
		f := installScopeFake(t, row,
			"80/tcp                     ALLOW IN    Anywhere",
			"443/tcp                    ALLOW IN    Anywhere")
		if err := MoveRule(1, "down"); err == nil {
			t.Errorf("MoveRule(%q) succeeded, want refusal", row)
		}
		if len(f.cmds) != 0 {
			t.Errorf("MoveRule(%q) ran %q, want no delete/insert", row, f.cmds)
		}
	}
}
