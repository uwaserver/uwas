package firewall

import (
	"errors"
	"strings"
	"testing"
)

// A delete/move that names the rule it means must refuse once BlockIP's
// insert at 1 has moved another rule to that number, and the check must be
// atomic with the change against a concurrent BlockIP (F68500, F68501).
func TestDeleteRuleExpectingRefusesRenumberedRule(t *testing.T) {
	s := seedRenumberSim(t)
	var r80 Rule
	for _, r := range GetStatus().Rules {
		if !r.V6 && r.Port == "80" {
			r80 = r
		}
	}
	if err := BlockIP("203.0.113.9", ""); err != nil {
		t.Fatal(err)
	}
	before := simRuleSet(s)
	if err := DeleteRuleExpecting(r80.Number, r80); !errors.Is(err, ErrRuleChanged) {
		t.Fatalf("stale delete: err=%v, want ErrRuleChanged", err)
	}
	if err := MoveRuleExpecting(r80.Number, "up", r80); !errors.Is(err, ErrRuleChanged) {
		t.Fatalf("stale move: err=%v, want ErrRuleChanged", err)
	}
	if got := simRuleSet(s); got != before {
		t.Fatalf("rules changed by refused requests\n got: %s\nwant: %s", got, before)
	}
}

func TestDeleteRuleExpectingAtomicWithConcurrentBlockIP(t *testing.T) {
	s := seedRenumberSim(t)
	var r80 Rule
	for _, r := range GetStatus().Rules {
		if !r.V6 && r.Port == "80" {
			r80 = r
		}
	}
	want := strings.ReplaceAll(withBlocked(simRuleSet(s)), "ALLOW 80/tcp from= v6=false; ", "")
	blocked := runAtFirstDelete(t, func() error { return BlockIP("203.0.113.9", "") })
	if err := DeleteRuleExpecting(r80.Number, r80); err != nil {
		t.Fatalf("DeleteRuleExpecting: %v", err)
	}
	if err := <-blocked; err != nil {
		t.Fatalf("BlockIP: %v", err)
	}
	if got := simRuleSet(s); got != want {
		t.Fatalf("rules after delete + concurrent block\n got: %s\nwant: %s", got, want)
	}
}
