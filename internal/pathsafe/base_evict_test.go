package pathsafe

import (
	"fmt"
	"testing"
	"time"
)

// Sweep removes expired entries so the target cache cannot grow without
// bound under a stream of unique URLs.
func TestSweepTargetCacheEvictsExpired(t *testing.T) {
	old := time.Now().Add(-time.Minute)
	keys := make([]string, 100)
	for i := range keys {
		keys[i] = fmt.Sprintf("/pathsafe-sweep-test/expired-%d", i)
		targetCache.Store(keys[i], &targetCacheEntry{resolved: keys[i], resolvedOK: true, time: old})
		targetCacheSize.Add(1)
	}

	sweepTargetCache()

	for _, k := range keys {
		if _, ok := targetCache.Load(k); ok {
			t.Fatalf("expired entry %q not evicted by sweep", k)
		}
	}
}

// If all entries are fresh (burst of unique URLs) and the cache is over
// capacity, the sweep clears the map entirely.
func TestSweepTargetCacheClearsWhenOverCap(t *testing.T) {
	now := time.Now()
	n := targetCacheMaxEntries + 10
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("/pathsafe-sweep-test/fresh-%d", i)
		if _, loaded := targetCache.Swap(k, &targetCacheEntry{resolved: k, resolvedOK: true, time: now}); !loaded {
			targetCacheSize.Add(1)
		}
	}

	sweepTargetCache()

	if _, ok := targetCache.Load("/pathsafe-sweep-test/fresh-0"); ok {
		t.Fatal("over-cap sweep of fresh entries must clear the cache")
	}
}

// Contains itself triggers the sweep once the approximate size counter
// crosses the cap (regression for unbounded targetCache growth).
func TestContainsTriggersSweepOverCap(t *testing.T) {
	t.Cleanup(func() {
		// Reset shared cache state so later tests start from a clean,
		// counter-consistent baseline.
		targetCache.Range(func(k, _ any) bool { targetCache.Delete(k); return true })
		targetCacheSize.Store(0)
	})

	root := t.TempDir()
	b, err := NewBase(root)
	if err != nil {
		t.Fatalf("NewBase: %v", err)
	}

	// Pre-fill the counter to just under the cap with expired entries so a
	// single Contains call pushes it over and sweeps them out.
	old := time.Now().Add(-time.Minute)
	seeded := make([]string, 50)
	for i := range seeded {
		seeded[i] = fmt.Sprintf("/pathsafe-sweep-test/trigger-%d", i)
		targetCache.Store(seeded[i], &targetCacheEntry{resolved: seeded[i], resolvedOK: true, time: old})
	}
	targetCacheSize.Store(targetCacheMaxEntries)

	if !b.Contains(root + "/some-file.txt") {
		t.Error("Contains returned false for path inside base")
	}

	for _, k := range seeded {
		if _, ok := targetCache.Load(k); ok {
			t.Fatalf("expired entry %q survived the Contains-triggered sweep", k)
		}
	}
}
