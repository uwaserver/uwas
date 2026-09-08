package firewall

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingStub records every ufw invocation and returns canned output for
// `ufw show added`. Safe for concurrent use because the rollback timer fires
// Disable from its own goroutine.
type recordingStub struct {
	mu       sync.Mutex
	calls    []string
	addedOut string
}

func (rs *recordingStub) install(t *testing.T) {
	t.Helper()
	origCmd, origPath, origOS := execCommandFn, execLookPathFn, runtimeGOOS
	t.Cleanup(func() {
		execCommandFn, execLookPathFn, runtimeGOOS = origCmd, origPath, origOS
		cancelRollback()
	})
	runtimeGOOS = "linux"
	execLookPathFn = func(string) (string, error) { return "/usr/sbin/ufw", nil }
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		rs.mu.Lock()
		rs.calls = append(rs.calls, name+" "+strings.Join(arg, " "))
		added := rs.addedOut
		rs.mu.Unlock()
		if name == "ufw" && len(arg) >= 2 && arg[0] == "show" && arg[1] == "added" {
			return exec.Command("printf", "%s", added)
		}
		return exec.Command("true")
	}
}

func (rs *recordingStub) got() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.calls...)
}

func (rs *recordingStub) contains(sub string) bool {
	for _, c := range rs.got() {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// Enabling must allow the given ports BEFORE flipping ufw on: a rule added
// after `ufw enable` races the connection the enable just started dropping.
func TestEnableWithRollbackAllowsPortsBeforeEnabling(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)

	if err := EnableWithRollback(time.Minute, []string{"22", "80", "443", "9443"}); err != nil {
		t.Fatalf("EnableWithRollback: %v", err)
	}
	calls := rs.got()

	var enableAt = -1
	allowed := map[string]int{}
	for i, c := range calls {
		if strings.Contains(c, "allow 22/tcp") {
			allowed["22"] = i
		}
		if strings.HasPrefix(c, "ufw --force enable") {
			enableAt = i
		}
	}
	if enableAt < 0 {
		t.Fatalf("ufw was never enabled: %v", calls)
	}
	for _, p := range []string{"22", "80", "443", "9443"} {
		if !rs.contains("allow " + p + "/tcp") {
			t.Errorf("port %s was not allowed", p)
		}
	}
	if allowed["22"] > enableAt {
		t.Errorf("SSH was allowed AFTER enabling — a remote session could be dropped first")
	}

	if pending, _ := rollbackStatus(); !pending {
		t.Fatal("a rollback should be pending after EnableWithRollback")
	}
	ConfirmEnable()
}

// Confirming cancels the rollback: ufw stays enabled, no disable is ever run.
func TestConfirmEnableCancelsRollback(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)

	if err := EnableWithRollback(50*time.Millisecond, []string{"22"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !ConfirmEnable() {
		t.Fatal("ConfirmEnable should report a pending rollback was cancelled")
	}
	// Wait past the original window; disable must NOT have fired.
	time.Sleep(120 * time.Millisecond)
	if rs.contains("ufw disable") {
		t.Fatal("firewall was disabled despite confirmation")
	}
	if pending, _ := rollbackStatus(); pending {
		t.Fatal("no rollback should be pending after confirmation")
	}
}

// The whole point: if nobody confirms, the firewall reverts itself, so an
// operator locked out by the enable gets back in.
func TestRollbackFiresWhenUnconfirmed(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)

	if err := EnableWithRollback(40*time.Millisecond, []string{"22"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rs.contains("ufw disable") {
			return // rolled back as intended
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rollback never disabled the firewall")
}

// A manual Disable clears a pending rollback, so its timer cannot later fire
// against a firewall the operator has since turned off (or back on by hand).
func TestManualDisableCancelsRollback(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)

	if err := EnableWithRollback(40*time.Millisecond, []string{"22"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if pending, _ := rollbackStatus(); pending {
		t.Fatal("manual Disable should have cancelled the pending rollback")
	}
}

func TestStagedRulesParsesShowAdded(t *testing.T) {
	rs := &recordingStub{addedOut: "Added user rules (see 'ufw status' for running firewall):\nufw allow 22/tcp\nufw allow 80/tcp\nufw deny from 203.0.113.5\n"}
	rs.install(t)

	rules := stagedRules()
	if len(rules) != 3 {
		t.Fatalf("expected 3 staged rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].Action != "ALLOW" || rules[0].Port != "22" || rules[0].Proto != "tcp" {
		t.Errorf("rule 0 = %+v, want ALLOW 22/tcp", rules[0])
	}
	if rules[2].Action != "DENY" || rules[2].From != "203.0.113.5" {
		t.Errorf("rule 2 = %+v, want DENY from 203.0.113.5", rules[2])
	}
}

// While ufw is inactive, `ufw status` lists nothing, so GetStatus must fall
// back to staged rules and flag them — otherwise the panel looks empty exactly
// when the operator is preparing rules before enabling.
func TestGetStatusShowsStagedRulesWhenInactive(t *testing.T) {
	rs := &recordingStub{addedOut: "ufw allow 22/tcp\nufw allow 443/tcp\n"}
	rs.install(t)
	// status returns inactive with no listed rules
	origCmd := execCommandFn
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		rs.mu.Lock()
		rs.calls = append(rs.calls, name+" "+strings.Join(arg, " "))
		added := rs.addedOut
		rs.mu.Unlock()
		if len(arg) >= 2 && arg[0] == "status" {
			return exec.Command("printf", "Status: inactive\n")
		}
		if len(arg) >= 2 && arg[0] == "show" && arg[1] == "added" {
			return exec.Command("printf", "%s", added)
		}
		return exec.Command("true")
	}
	_ = origCmd

	st := GetStatus()
	if st.Active {
		t.Fatal("status should be inactive")
	}
	if !st.Staged {
		t.Fatal("inactive status with staged rules must set Staged=true")
	}
	if len(st.Rules) != 2 {
		t.Fatalf("expected 2 staged rules surfaced, got %d", len(st.Rules))
	}
}
