package firewall

import (
	"os/exec"
	"strings"
	"testing"
)

// stubExec records the commands that would have been run and reports success.
func stubExec(t *testing.T, found ...string) *[]string {
	t.Helper()
	var calls []string

	origCmd, origPath, origOS := execCommandFn, execLookPathFn, runtimeGOOS
	t.Cleanup(func() { execCommandFn, execLookPathFn, runtimeGOOS = origCmd, origPath, origOS })

	runtimeGOOS = "linux"
	available := map[string]bool{}
	for _, f := range found {
		available[f] = true
	}
	execLookPathFn = func(file string) (string, error) {
		if available[file] {
			return "/usr/sbin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(arg, " "))
		return exec.Command("true")
	}
	return &calls
}

// Blocking any of these takes the server off the network or walls the operator
// out of it, so they are refused before a command is ever built.
func TestBlockIPRefusesDangerousAddresses(t *testing.T) {
	calls := stubExec(t, "ufw")

	for _, ip := range []string{
		"127.0.0.1", "::1",
		"10.0.0.5", "192.168.1.1", "172.16.0.1",
		"169.254.169.254",
		"0.0.0.0",
		"224.0.0.1",
		"not-an-ip", "",
	} {
		if err := BlockIP(ip, "test"); err == nil {
			t.Errorf("BlockIP(%q) should have been refused", ip)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused address must not reach the firewall, got %v", *calls)
	}
}

// The rule has to be inserted at the top: ufw stops at the first match, so a
// deny appended after "ALLOW 443/tcp" would never be evaluated.
func TestBlockIPInsertsAtTopOfUFW(t *testing.T) {
	calls := stubExec(t, "ufw")

	if err := BlockIP("203.0.113.5", "uwas-autoblock"); err != nil {
		t.Fatalf("BlockIP: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected one ufw call, got %v", *calls)
	}
	got := (*calls)[0]
	if !strings.HasPrefix(got, "ufw insert 1 deny from 203.0.113.5") {
		t.Fatalf("rule not inserted at position 1: %q", got)
	}
	if !strings.Contains(got, "comment uwas-autoblock") {
		t.Fatalf("rule missing its comment tag: %q", got)
	}
}

func TestUnblockIPUsesUFWDelete(t *testing.T) {
	calls := stubExec(t, "ufw")

	if err := UnblockIP("203.0.113.5"); err != nil {
		t.Fatalf("UnblockIP: %v", err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "delete deny from 203.0.113.5") {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestFallsBackToIPTablesWhenUFWAbsent(t *testing.T) {
	calls := stubExec(t, "iptables", "ip6tables")

	if err := BlockIP("203.0.113.6", ""); err != nil {
		t.Fatalf("BlockIP: %v", err)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "iptables -I INPUT 1 -s 203.0.113.6 -j DROP") {
		t.Fatalf("unexpected calls: %v", *calls)
	}

	calls2 := stubExec(t, "iptables", "ip6tables")
	if err := BlockIP("2001:db8::1", ""); err != nil {
		t.Fatalf("BlockIP v6: %v", err)
	}
	if len(*calls2) != 1 || !strings.HasPrefix((*calls2)[0], "ip6tables -I INPUT 1 -s 2001:db8::1 -j DROP") {
		t.Fatalf("IPv6 must use ip6tables, got %v", *calls2)
	}
}

func TestNoBackendReportsError(t *testing.T) {
	stubExec(t) // nothing installed

	if err := BlockIP("203.0.113.7", ""); err == nil {
		t.Fatal("expected an error with no firewall backend")
	}
	if err := UnblockIP("203.0.113.7"); err == nil {
		t.Fatal("expected an error with no firewall backend")
	}
}

func TestListBlockedIPsReturnsOnlyAutoblockRules(t *testing.T) {
	origCmd, origPath, origOS := execCommandFn, execLookPathFn, runtimeGOOS
	t.Cleanup(func() { execCommandFn, execLookPathFn, runtimeGOOS = origCmd, origPath, origOS })

	runtimeGOOS = "linux"
	execLookPathFn = func(string) (string, error) { return "/usr/sbin/ufw", nil }
	execCommandFn = func(string, ...string) *exec.Cmd {
		return exec.Command("printf", `Status: active

[ 1] Anywhere                   DENY IN     203.0.113.5                # uwas-autoblock
[ 2] Anywhere                   DENY IN     198.51.100.9               # operator rule
[ 3] 443/tcp                    ALLOW IN    Anywhere
`)
	}

	got := ListBlockedIPs()
	if len(got) != 1 || got[0] != "203.0.113.5" {
		// An operator's own deny rule must survive: expiry deletes what this
		// returns, and deleting a hand-written rule would be a silent
		// security regression.
		t.Fatalf("ListBlockedIPs() = %v, want only the autoblock rule", got)
	}
}
