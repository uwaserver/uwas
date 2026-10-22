package autoblock

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// gateHandler blocks the goroutine that logs msg until release is closed, and
// tells the test it got there. It lets the test run another goroutine's work in
// the exact gap between expire() dropping a block and queueing its rule
// removal, with no sleeps.
type gateHandler struct {
	slog.Handler
	msg     string
	reached chan struct{}
	release chan struct{}
}

func (g *gateHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == g.msg {
		close(g.reached)
		<-g.release
	}
	return g.Handler.Handle(ctx, r)
}

// A block that expires and is re-imposed before expire() has queued the old
// rule's removal must keep its firewall rule. The removal used to be queued
// after the new add, so the worker ran add, then remove: the kernel rule was
// gone while the entry still said Firewall=true and the block was live.
func TestExpireDoesNotRemoveRuleOfReimposedBlock(t *testing.T) {
	g := &gateHandler{
		Handler: slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelDebug}),
		msg:     "autoblock expired",
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	cfg := Config{Enabled: true, Window: time.Minute, BlockDuration: time.Hour, FirewallSync: true}
	b := New(cfg, &logger.Logger{Logger: slog.New(g)})

	// The kernel rule set: ufw skips an add of an existing rule, and a delete
	// removes it, so presence is a boolean, not a count.
	rule := map[string]bool{}
	b.SetFirewall(
		func(ip, _ string) error { rule[ip] = true; return nil },
		func(ip string) error { rule[ip] = false; return nil })
	drain := func() {
		for {
			select {
			case op := <-b.fwQueue:
				b.runFirewallOp(op)
			default:
				return
			}
		}
	}

	const ip = "198.51.100.77"
	if err := b.Block(ip, "", 0); err != nil {
		t.Fatal(err)
	}
	drain() // rule installed

	a := mustAddr(t, ip)
	b.mu.Lock()
	b.blocked[a].ExpiresAt = time.Now().Add(-time.Second) // term is up
	b.mu.Unlock()

	done := make(chan struct{})
	go func() { b.expire(); close(done) }()
	<-g.reached // expire dropped the block; its removal is not queued yet

	if err := b.Block(ip, "", 0); err != nil { // offender is blocked again
		t.Fatal(err)
	}
	close(g.release)
	<-done
	drain()

	if !b.Blocked(a) {
		t.Fatal("control: block must be live")
	}
	t.Logf("EXPECTED: rule present=true for a live block")
	t.Logf("ACTUAL:   rule present=%v entry.Firewall=%v", rule[ip], b.List()[0].Firewall)
	if !rule[ip] {
		t.Fatal("PROBLEM CONFIRMED: stale removal lifted the rule of a live block")
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Controls and edges for the fix: an expiry nobody re-blocked still removes
// its rule, a removal of an unrelated IP is untouched, a dry-run entry never
// protects a rule, and the removal runs after the block is really gone.
func TestRunFirewallRemoveEdges(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.FirewallSync = true })
	rule := map[string]bool{}
	b.SetFirewall(
		func(ip, _ string) error { rule[ip] = true; return nil },
		func(ip string) error { rule[ip] = false; return nil })
	drain := func() {
		for {
			select {
			case op := <-b.fwQueue:
				b.runFirewallOp(op)
			default:
				return
			}
		}
	}

	// Control: expire with no re-block removes the rule.
	const gone = "198.51.100.80"
	if err := b.Block(gone, "", 0); err != nil {
		t.Fatal(err)
	}
	drain()
	b.mu.Lock()
	b.blocked[mustAddr(t, gone)].ExpiresAt = time.Now().Add(-time.Second)
	b.mu.Unlock()
	b.expire()
	drain()
	if rule[gone] {
		t.Fatal("expired block kept its firewall rule")
	}

	// Unblock still removes.
	const un = "198.51.100.81"
	if err := b.Block(un, "", 0); err != nil {
		t.Fatal(err)
	}
	drain()
	if err := b.Unblock(un); err != nil {
		t.Fatal(err)
	}
	drain()
	if rule[un] {
		t.Fatal("unblocked IP kept its firewall rule")
	}

	// A live block's rule survives a stale removal; an unrelated rule does not.
	const live, other = "198.51.100.82", "198.51.100.83"
	rule[other] = true
	if err := b.Block(live, "", 0); err != nil {
		t.Fatal(err)
	}
	drain()
	b.fwQueue <- fwOp{ip: live, remove: true}
	b.fwQueue <- fwOp{ip: other, remove: true}
	drain()
	if !rule[live] {
		t.Fatal("stale removal lifted a live block's rule")
	}
	if rule[other] {
		t.Fatal("removal of an unblocked IP was skipped")
	}

	// A dry-run entry does not own a rule: the removal goes through.
	const dry = "198.51.100.84"
	a := mustAddr(t, dry)
	rule[dry] = true
	b.mu.Lock()
	b.blocked[a] = &Entry{IP: dry, DryRun: true}
	b.mu.Unlock()
	b.fwQueue <- fwOp{ip: dry, remove: true}
	drain()
	if rule[dry] {
		t.Fatal("dry-run entry protected a firewall rule")
	}

	// Unparseable IP: removal is attempted, not swallowed.
	b.fwQueue <- fwOp{ip: "not-an-ip", remove: true}
	drain()
	if v, ok := rule["not-an-ip"]; !ok || v {
		t.Fatalf("removal of an unparseable IP not attempted: %v %v", v, ok)
	}
}
