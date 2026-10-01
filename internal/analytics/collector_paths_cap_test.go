package analytics

import (
	"fmt"
	"testing"
)

// pathsCap mirrors the unexported maxPaths cap inside RecordFull. It is
// deliberately a test-local literal so a change to the production cap fails
// this contract instead of silently riding along with it.
const pathsCap = 50000

// statsForHost returns the recorded DomainStats for host, failing if absent.
func statsForHost(t *testing.T, c *Collector, host string) *DomainStats {
	t.Helper()
	v, ok := c.domains.Load(host)
	if !ok {
		t.Fatalf("no stats recorded for host %q", host)
	}
	return v.(*DomainStats)
}

func pathCount(stats *DomainStats, path string) int64 {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	return stats.Paths[path]
}

// TestRecordFullKeepsCountingExistingPathsPastCap pins the documented cap
// contract from collector.go: "Existing keys still update; only new keys are
// dropped past the cap."
//
// Before the fix stats.Paths was guarded by `len(stats.Paths) < maxPaths`
// alone — unlike its UniqueIPs/Referrers/UserAgents siblings, which all use
// `existing-key OR under-cap`. So the moment a site crossed maxPaths distinct
// paths the map froze completely, including keys it already held, and the
// dashboard's TopPaths ranking was frozen for the life of the process.
func TestRecordFullKeepsCountingExistingPathsPastCap(t *testing.T) {
	c := New()
	const host = "capsite.example.com"

	for i := 0; i < pathsCap; i++ {
		c.RecordFull(host, fmt.Sprintf("/p%d", i), "10.0.0.1", "", "curl/8", 200, 10)
	}
	stats := statsForHost(t, c, host)

	stats.mu.Lock()
	size := len(stats.Paths)
	stats.mu.Unlock()
	if size != pathsCap {
		t.Fatalf("setup: expected Paths to be exactly at the cap (%d), got %d", pathsCap, size)
	}
	if got := pathCount(stats, "/p0"); got != 1 {
		t.Fatalf("setup: expected /p0 counted once before the cap probe, got %d", got)
	}

	// /p0 is ALREADY tracked, and the map is now at the cap.
	c.RecordFull(host, "/p0", "10.0.0.2", "", "curl/8", 200, 10)

	if got := pathCount(stats, "/p0"); got != 2 {
		t.Errorf("an existing path must keep counting past the cap; /p0 = %d, want 2", got)
	}
}

// TestRecordFullStillDropsNewPathsPastCap is the boundary that matters most:
// relaxing the guard must NOT have turned the cap into unbounded growth. The
// cap exists so a client sending many distinct paths cannot exhaust the
// process's memory, so a genuinely new key past the cap must still be ignored.
func TestRecordFullStillDropsNewPathsPastCap(t *testing.T) {
	c := New()
	const host = "dropper.example.com"

	for i := 0; i < pathsCap; i++ {
		c.RecordFull(host, fmt.Sprintf("/p%d", i), "10.0.0.1", "", "curl/8", 200, 10)
	}
	stats := statsForHost(t, c, host)

	// A key never seen before, recorded while the map is at the cap.
	c.RecordFull(host, "/brand-new-path", "10.0.0.2", "", "curl/8", 200, 10)

	if got := pathCount(stats, "/brand-new-path"); got != 0 {
		t.Errorf("a new path past the cap must be dropped to bound memory; "+
			"/brand-new-path = %d, want 0", got)
	}

	stats.mu.Lock()
	size := len(stats.Paths)
	stats.mu.Unlock()
	if size != pathsCap {
		t.Errorf("Paths must not grow past the cap; len = %d, want %d", size, pathsCap)
	}
}

// TestRecordFullCountsPathsUnderCap is the neighbouring valid case: below the
// cap every path counts normally. Must hold before and after the fix.
func TestRecordFullCountsPathsUnderCap(t *testing.T) {
	c := New()
	const host = "small.example.com"

	for i := 0; i < 10; i++ {
		c.RecordFull(host, fmt.Sprintf("/p%d", i), "10.0.0.1", "", "curl/8", 200, 10)
	}
	stats := statsForHost(t, c, host)

	c.RecordFull(host, "/p0", "10.0.0.2", "", "curl/8", 200, 10)
	if got := pathCount(stats, "/p0"); got != 2 {
		t.Errorf("under the cap an existing path must count normally; /p0 = %d, want 2", got)
	}
}

// TestRecordFullReferrersStillCountPastCap is the asymmetry control: Referrers is
// capped with the same maxDistinct value and was always correct. It proves the
// cap itself is not the defect, and that the fix did not change its behaviour.
//
// UserAgents cannot serve this role because RecordFull buckets user-agents into
// a browser family via classifyUA, so many distinct UA strings collapse to a
// single entry and never reach the cap.
func TestRecordFullReferrersStillCountPastCap(t *testing.T) {
	c := New()
	const host = "refsite.example.com"

	for i := 0; i < pathsCap; i++ {
		c.RecordFull(host, "/only", "10.0.0.1", fmt.Sprintf("https://r%d.example/p", i), "curl/8", 200, 10)
	}
	stats := statsForHost(t, c, host)

	stats.mu.Lock()
	size := len(stats.Referrers)
	before := stats.Referrers["r0.example"]
	stats.mu.Unlock()
	if size != pathsCap {
		t.Fatalf("setup: expected Referrers to be at the cap (%d), got %d", pathsCap, size)
	}
	if before == 0 {
		t.Fatal("setup: expected r0.example to be tracked before the cap probe")
	}

	c.RecordFull(host, "/only", "10.0.0.1", "https://r0.example/p", "curl/8", 200, 10)

	stats.mu.Lock()
	after := stats.Referrers["r0.example"]
	stats.mu.Unlock()
	if after == before {
		t.Errorf("Referrers must keep counting existing keys past the cap; "+
			"r0.example stayed at %d", after)
	}
}
