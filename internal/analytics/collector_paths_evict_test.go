package analytics

import (
	"fmt"
	"testing"
)

// A scanner that probes 50 000 distinct one-off paths fills the per-domain Paths
// map. Before the fix every later new path was dropped for the life of the
// process, so a page published after the scan could never show up in the
// dashboard's TopPaths, however popular it became.
func TestRecordFullNewPopularPathSurvivesScannerFlood(t *testing.T) {
	c := New()
	const host = "flooded.example.com"

	for i := 0; i < pathsCap; i++ {
		c.RecordFull(host, fmt.Sprintf("/scan%d", i), "203.0.113.9:1", "", "curl/8", 404, 1)
	}
	// The flood goes on while real visitors arrive at a page that is new.
	for i := 0; i < 8000; i++ {
		c.RecordFull(host, fmt.Sprintf("/scan-more%d", i), "203.0.113.9:1", "", "curl/8", 404, 1)
		if i%4 == 0 {
			c.RecordFull(host, "/new-post", "198.51.100.7:2", "", "curl/8", 200, 1)
		}
	}
	stats := statsForHost(t, c, host)
	got := pathCount(stats, "/new-post")
	t.Logf("EXPECTED: /new-post counted (>0)\nACTUAL: %d", got)
	if got == 0 {
		t.Fatalf("PROBLEM CONFIRMED: a new popular path was never counted after the cap filled")
	}
	stats.mu.Lock()
	size := len(stats.Paths)
	stats.mu.Unlock()
	if size > pathsCap {
		t.Errorf("Paths grew past the cap: %d > %d", size, pathsCap)
	}
	if top := c.GetHost(host).TopPaths; top["/new-post"] == 0 {
		t.Errorf("/new-post missing from TopPaths: %v", top)
	}
}

// Paths that real traffic keeps hitting must survive the clean-up of one-hit
// paths, and the map never exceeds the cap (memory stays bounded).
func TestRecordFullPathEvictionKeepsRepeatedPathsAndBound(t *testing.T) {
	c := New()
	const host = "bounded.example.com"

	c.RecordFull(host, "/popular", "198.51.100.7:2", "", "curl/8", 200, 1)
	c.RecordFull(host, "/popular", "198.51.100.7:2", "", "curl/8", 200, 1)
	for i := 0; i < pathsCap+20000; i++ {
		c.RecordFull(host, fmt.Sprintf("/junk%d", i), "203.0.113.9:1", "", "curl/8", 404, 1)
	}
	stats := statsForHost(t, c, host)
	if got := pathCount(stats, "/popular"); got != 2 {
		t.Errorf("/popular = %d, want 2 (a repeated path must survive the clean-up)", got)
	}
	stats.mu.Lock()
	size := len(stats.Paths)
	stats.mu.Unlock()
	if size > pathsCap {
		t.Errorf("Paths len = %d, want <= %d", size, pathsCap)
	}
}

// Control: with no flood the clean-up never runs and nothing changes.
func TestRecordFullPathEvictionIdleBelowCap(t *testing.T) {
	c := New()
	const host = "quiet.example.com"
	for i := 0; i < 100; i++ {
		c.RecordFull(host, fmt.Sprintf("/p%d", i), "198.51.100.7:2", "", "curl/8", 200, 1)
	}
	stats := statsForHost(t, c, host)
	stats.mu.Lock()
	size := len(stats.Paths)
	stats.mu.Unlock()
	if size != 100 {
		t.Errorf("Paths len = %d, want 100", size)
	}
}
