package autoblock

import "testing"

// A rule removal that finds the firewall queue full must not be dropped: the
// block is already gone from memory and the state file, so nothing else would
// ever delete the kernel deny rule and the IP would stay banned for good.
func TestUnblockWithFullFirewallQueueStillRemovesRule(t *testing.T) {
	b := testBlocker(t, func(c *Config) { c.FirewallSync = true })
	removed := map[string]int{}
	b.SetFirewall(func(string, string) error { return nil },
		func(ip string) error { removed[ip]++; return nil })
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

	const ip = "198.51.100.9"
	if err := b.Block(ip, "", 0); err != nil {
		t.Fatal(err)
	}
	for len(b.fwQueue) < cap(b.fwQueue) {
		b.fwQueue <- fwOp{ip: "198.51.100.200"}
	}
	if err := b.Unblock(ip); err != nil {
		t.Fatal(err)
	}
	drain()
	b.expire() // the periodic tick retries deferred removals
	drain()
	if removed[ip] != 1 {
		t.Fatalf("firewall rule for %s removed %d times, want 1", ip, removed[ip])
	}

	// Re-blocked before the retry: the stale removal must not lift the new rule.
	if err := b.Block(ip, "", 0); err != nil {
		t.Fatal(err)
	}
	for len(b.fwQueue) < cap(b.fwQueue) {
		b.fwQueue <- fwOp{ip: "198.51.100.200"}
	}
	if err := b.Unblock(ip); err != nil {
		t.Fatal(err)
	}
	drain()
	if err := b.Block(ip, "", 0); err != nil {
		t.Fatal(err)
	}
	drain()
	b.expire()
	drain()
	if removed[ip] != 1 {
		t.Fatalf("stale removal lifted the re-blocked rule for %s", ip)
	}
}
