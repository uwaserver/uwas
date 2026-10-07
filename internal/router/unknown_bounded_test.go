package router

// Regression guard for the unknown-host tracker's memory bound.
//
// Record() is fed the raw Host header on the pre-auth 421 path
// (internal/server/server.go), so an anonymous client could grow t.hosts without
// limit by varying one header, and — because List() sorts by Hits — bury the
// real scanner signal under a flood of one-hit hosts.
//
// These assert the bound and, just as importantly, that bounding did not cost
// the tracker's existing behaviour: repeated hits still accumulate, and an
// operator-applied block is never dropped by eviction.

import (
	"fmt"
	"testing"
)

// TestUnknownHostTrackerBoundsDistinctHosts asserts that one anonymous request
// per distinct Host header cannot grow the tracker without limit.
func TestUnknownHostTrackerBoundsDistinctHosts(t *testing.T) {
	tr := NewUnknownHostTracker()

	const flood = 50000
	for i := 0; i < flood; i++ {
		tr.Record(fmt.Sprintf("probe-%d.attacker.example", i))
	}

	got := len(tr.List())
	if got > maxTrackedHosts {
		t.Fatalf("%d distinct Host headers grew the tracker to %d entries (bound %d)",
			flood, got, maxTrackedHosts)
	}
}

// TestUnknownHostTrackerRepeatedHostAccumulates is the control: ordinary traffic
// against one host must stay a single entry and keep counting. If this fails the
// bound is costing real behaviour, not just memory.
func TestUnknownHostTrackerRepeatedHostAccumulates(t *testing.T) {
	tr := NewUnknownHostTracker()

	const repeats = 1000
	for i := 0; i < repeats; i++ {
		tr.Record("scanner.example.com")
	}

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("one repeated host should be 1 entry, got %d", len(entries))
	}
	if entries[0].Hits != repeats {
		t.Fatalf("repeated host should accumulate %d hits, got %d", repeats, entries[0].Hits)
	}
}

// TestUnknownHostTrackerEvictionKeepsActiveScanner covers the branch the fix
// added: eviction drops the least-recently-seen hosts, so a host still being
// scanned must survive a flood of one-hit hosts, and it must keep its hits.
func TestUnknownHostTrackerEvictionKeepsActiveScanner(t *testing.T) {
	tr := NewUnknownHostTracker()

	// A scanner that keeps hitting across the flood stays "recently seen".
	for i := 0; i < maxTrackedHosts; i++ {
		tr.Record(fmt.Sprintf("probe-%d.attacker.example", i))
		if i%1000 == 0 {
			tr.Record("live-scanner.example.com")
		}
	}
	tr.Record("live-scanner.example.com")

	found := false
	for _, e := range tr.List() {
		if e.Host == "live-scanner.example.com" {
			found = true
			if e.Hits < 2 {
				t.Fatalf("active scanner lost its hit count: got %d", e.Hits)
			}
			break
		}
	}
	if !found {
		t.Fatal("an actively-scanned host was evicted by a one-hit flood")
	}
}

// TestUnknownHostTrackerEvictionNeverDropsBlockedHost pins the safety property
// the eviction loop must preserve: eviction bounds memory, it never unblocks.
// A blocked host must remain blocked even when the map is swept.
func TestUnknownHostTrackerEvictionNeverDropsBlockedHost(t *testing.T) {
	tr := NewUnknownHostTracker()

	tr.Record("blocked.example.com")
	tr.Block("blocked.example.com")

	// Flood well past the bound, keeping the blocked host quiet so it is among
	// the least-recently-seen and therefore an eviction candidate.
	for i := 0; i < maxTrackedHosts*2; i++ {
		tr.Record(fmt.Sprintf("probe-%d.attacker.example", i))
	}

	if !tr.IsBlocked("blocked.example.com") {
		t.Fatal("a blocked host was dropped from the block set by eviction")
	}
	// And it must still report blocked=true to the rejection path, whether or
	// not its tracking entry survived the sweep.
	if tr.Record("blocked.example.com") != true {
		t.Fatal("a blocked unknown host must report blocked=true to the 421 path")
	}
}
