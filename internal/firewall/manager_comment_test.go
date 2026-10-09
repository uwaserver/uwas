package firewall

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// `ufw status numbered` appends "# <comment>" to commented rules, and every
// autoblock deny carries one. Folding it into From made MoveRule delete the
// rule and then fail to re-insert "203.0.113.5 # uwas-autoblock", dropping
// the block.
func TestParseUFWRuleSplitsComment(t *testing.T) {
	tests := []struct {
		line, from, port, comment string
		v6                        bool
	}{
		{"[ 1] Anywhere                   DENY IN     203.0.113.5                # uwas-autoblock", "203.0.113.5", "", "uwas-autoblock", false},
		{"[ 2] 80/tcp                     ALLOW IN    Anywhere                   # web", "Anywhere", "80", "web", false},
		{"[ 3] 22/tcp (v6)                ALLOW IN    Anywhere (v6)              # SSH", "Anywhere", "22", "SSH", true},
		{"[ 4] Anywhere                   DENY IN     203.0.113.5 #", "203.0.113.5", "", "", false},
		{"[ 5] Anywhere                   DENY IN     203.0.113.5", "203.0.113.5", "", "", false},
	}
	for _, tt := range tests {
		r := parseUFWRule(tt.line)
		if r.From != tt.from || r.Port != tt.port || r.Comment != tt.comment || r.V6 != tt.v6 {
			t.Errorf("parseUFWRule(%q) = From %q Port %q Comment %q V6 %v; want %q %q %q %v",
				tt.line, r.From, r.Port, r.Comment, r.V6, tt.from, tt.port, tt.comment, tt.v6)
		}
	}
}

func TestMoveRuleKeepsCommentedAutoblockDeny(t *testing.T) {
	origCmd, origPath, origOS := execCommandFn, execLookPathFn, runtimeGOOS
	t.Cleanup(func() { execCommandFn, execLookPathFn, runtimeGOOS = origCmd, origPath, origOS })
	runtimeGOOS = "linux"
	execLookPathFn = func(string) (string, error) { return "/usr/sbin/ufw", nil }
	status := "Status: active\n\n" +
		"[ 1] Anywhere                   DENY IN     203.0.113.5                # uwas-autoblock\n" +
		"[ 2] 80/tcp                     ALLOW IN    Anywhere\n" +
		"[ 3] Anywhere                   DENY IN     Anywhere\n"
	var mu sync.Mutex
	var inserts [][]string
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		if len(arg) > 0 && arg[0] == "status" {
			return exec.Command("printf", "%s", status)
		}
		if len(arg) > 0 && arg[0] == "insert" {
			mu.Lock()
			inserts = append(inserts, arg)
			mu.Unlock()
		}
		return exec.Command("true")
	}

	if err := MoveRule(1, "down"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inserts) != 1 {
		t.Fatalf("inserts = %v, want exactly one", inserts)
	}
	want := "insert 3 deny from 203.0.113.5 comment uwas-autoblock"
	if got := strings.Join(inserts[0], " "); got != want {
		t.Fatalf("re-insert = %q, want %q", got, want)
	}
}
