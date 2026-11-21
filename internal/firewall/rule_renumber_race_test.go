package firewall

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
)

// simRuleSet lists every simulated rule, sorted, so a lost or duplicated rule
// shows regardless of order.
func simRuleSet(s *simState) string {
	s.mu.Lock()
	s.load()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.rules {
		out = append(out, fmt.Sprintf("%s %s/%s from=%s v6=%v", r.Action, r.Port, r.Proto, r.From, r.V6))
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// runAtFirstDelete runs fn concurrently when the first `ufw --force delete N`
// is issued and lets that delete proceed once fn finished or is waiting for
// the rule lock — so fn lands exactly between a status read and the deletes
// computed from it.
func runAtFirstDelete(t *testing.T, fn func() error) <-chan error {
	t.Helper()
	sim := execCommandFn
	done := make(chan error, 1)
	waiting := make(chan struct{}, 1)
	prev := testRulesLockContended
	testRulesLockContended = func() {
		select {
		case waiting <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { testRulesLockContended = prev })
	var once sync.Once
	res := make(chan error, 1)
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		if len(arg) == 3 && arg[0] == "--force" && arg[1] == "delete" && isNumericPort(arg[2]) {
			once.Do(func() {
				go func() { done <- fn() }()
				select {
				case err := <-done:
					res <- err
				case <-waiting:
					go func() { res <- <-done }()
				}
			})
		}
		return sim(name, arg...)
	}
	return res
}

// withBlocked adds the rule BlockIP("203.0.113.9") creates to a simRuleSet.
func withBlocked(set string) string {
	xs := append(strings.Split(set, "; "), "DENY / from=203.0.113.9 v6=false")
	sort.Strings(xs)
	return strings.Join(xs, "; ")
}

func seedRenumberSim(t *testing.T) *simState {
	t.Helper()
	s := newSim(t)
	s.add("allow", "22", "tcp", "", false)
	s.add("allow", "80", "tcp", "", false)
	s.add("allow", "443", "tcp", "", false)
	s.add("deny", "", "", "", false)
	s.setActive(true)
	installSim(t, s)
	return s
}

// An autoblock BlockIP (`insert 1`, renumbering every rule) landing between
// MoveRule's status read and its deletes made MoveRule delete the wrong rules —
// here the IPv6 SSH allow — and leave a duplicate (F67700).
func TestMoveRuleNotRenumberedByConcurrentBlockIP(t *testing.T) {
	s := seedRenumberSim(t)
	want := withBlocked(simRuleSet(s))

	n80 := 0
	for _, r := range GetStatus().Rules {
		if !r.V6 && r.Port == "80" {
			n80 = r.Number
		}
	}
	blocked := runAtFirstDelete(t, func() error { return BlockIP("203.0.113.9", "") })
	if err := MoveRule(n80, "up"); err != nil {
		t.Fatalf("MoveRule: %v", err)
	}
	if err := <-blocked; err != nil {
		t.Fatalf("BlockIP: %v", err)
	}
	if got := simRuleSet(s); got != want {
		t.Fatalf("rules after move + concurrent block\n got: %s\nwant: %s", got, want)
	}
}

// The same for DeduplicateRules: only the duplicate may go.
func TestDeduplicateRulesNotRenumberedByConcurrentBlockIP(t *testing.T) {
	s := seedRenumberSim(t)
	want := withBlocked(simRuleSet(s))
	s.add("allow", "80", "tcp", "", false) // duplicate v4+v6

	blocked := runAtFirstDelete(t, func() error { return BlockIP("203.0.113.9", "") })
	DeduplicateRules()
	if err := <-blocked; err != nil {
		t.Fatalf("BlockIP: %v", err)
	}
	if got := simRuleSet(s); got != want {
		t.Fatalf("rules after dedup + concurrent block\n got: %s\nwant: %s", got, want)
	}
}
