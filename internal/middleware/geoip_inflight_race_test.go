package middleware

import (
	"fmt"
	"sync"
	"testing"
)

// TestGeoCacheInflightReleaseRace is a regression test for a data race on
// geoCache.inflight.
//
// releaseInflight() used to delete from c.inflight WITHOUT holding c.mu —
// its comment assumed a caller-held lock that no caller actually held:
//
//	geoPoolStart worker:     job.cache.set(...)                  // locks/unlocks internally
//	                         job.cache.releaseInflight(job.ip)   // no lock held
//	enqueueGeoLookup full-Q: cache.releaseInflight(ip)           // no lock held
//
// while tryClaimInflight() reads and writes c.inflight under c.mu. An
// unlocked delete concurrent with a locked write (or another unlocked
// delete) on the same Go map is a data race, and in non-race builds a
// potential unrecoverable "concurrent map writes" fatal that crashes the
// whole server. Reachable on any geo-blocked site: every uncached client IP
// claims (locked) while workers complete lookups and release (unlocked).
// releaseInflight now takes c.mu itself; run with -race to catch a
// regression of that guard.
//
// The test executes the exact production sequences verbatim;
// lookupExternal's result only feeds set's arguments, so the network call is
// omitted — the racing statements are set()+releaseInflight() vs
// tryClaimInflight().
func TestGeoCacheInflightReleaseRace(t *testing.T) {
	cache := &geoCache{
		entries:  make(map[string]geoCacheEntry),
		inflight: make(map[string]struct{}),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	// Goroutine A: the geoPoolStart worker sequence, verbatim — set() then
	// releaseInflight().
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			jobIP := fmt.Sprintf("10.1.%d.%d", (i/254)%254, i%254+1)
			cache.set(jobIP, "US")
			cache.releaseInflight(jobIP)
		}
	}()

	// Goroutine B: request goroutines — enqueueGeoLookup's tryClaimInflight
	// (locked write) and its queue-full release path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			reqIP := fmt.Sprintf("10.2.%d.%d", (i/254)%254, i%254+1)
			if cache.tryClaimInflight(reqIP) {
				cache.releaseInflight(reqIP)
			}
		}
	}()

	close(start)
	wg.Wait()

	// Control: concurrent get/set on entries are fully locked on every path
	// and must be race-free regardless of the fix — proving the test flags
	// the unlocked inflight delete specifically, not locked operations in
	// general.
	var wg2 sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg2.Add(1)
		go func(g int) {
			defer wg2.Done()
			for i := 0; i < 1000; i++ {
				ip := fmt.Sprintf("10.3.%d.%d", g, i%254+1)
				cache.set(ip, "DE")
				cache.get(ip)
			}
		}(g)
	}
	wg2.Wait()
}
