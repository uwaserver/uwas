// Fixed race regression test for IPSet.netsFor() write-write data race.
//
// Bug: netsFor() (iplist.go:45-68) releases the read lock, recomputes
// the CIDR parse (unprotected), then acquires the write lock to write
// s.nets. When multiple goroutines call Contains() with different CIDRs,
// they all miss the cache and recompute concurrently, then race to Lock
// and write s.nets simultaneously — a write-write data race on the map.
//
// Original bug: the test used the SAME CIDR for all goroutines, so
// s.fingerprint always matched and netsFor never entered the recompute path.
// The test was a false negative — it could never catch the regression.
//
// Fix: each goroutine uses a different CIDR, guaranteeing a cache miss
// and the recompute path for every call, creating the write-write race.
package cloudflare

import (
	"sync"
	"testing"
	"time"
)

// TestIPSetContainsRace proves a write-write data race in netsFor.
//
// Race: when multiple goroutines call Contains() with different CIDRs concurrently,
// all netsFor() calls see a cache miss, recompute unprotected, then ALL call
// s.mu.Lock() and write s.nets simultaneously — write-write on the map header.
//
// The race detector observes this as a DATA RACE.
//
// Pre-fix: race detector reports DATA RACE on s.nets (exit 1).
// Post-fix: Lock serializes all writes; no concurrent write (exit 0, no races).
func TestIPSetContainsRace(t *testing.T) {
	set := NewIPSet()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	start := make(chan struct{})

	// Each goroutine uses a DIFFERENT CIDR — this forces ALL of them to
	// miss the cache (fingerprint mismatch) and enter the recompute path.
	// Without this, if all goroutines use the same CIDR, the cache hit
	// short-circuits netsFor and the race window is never entered.
	cidrs := []string{
		"10.0.0.0/8",    // goroutine 0
		"172.16.0.0/12", // goroutine 1
		"192.168.0.0/16", // goroutine 2
		"8.8.0.0/16",    // goroutine 3
		"1.0.0.0/24",    // goroutine 4
		"2.2.0.0/24",    // goroutine 5
		"3.3.0.0/24",    // goroutine 6
		"4.4.0.0/24",    // goroutine 7
	}

	for i := 0; i < len(cidrs); i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // barrier: all goroutines ready before starting
			for {
				select {
				case <-stop:
					return
				default:
					// Each goroutine's fixed CIDR is DIFFERENT from the others,
					// so every Contains call triggers a fingerprint mismatch and
					// enters the unprotected recompute path → write-write race.
					cidr := cidrs[idx]
					_ = set.Contains("10.0.0.1", []string{cidr})
				}
			}
		}(i)
	}

	close(start)      // release barrier — all goroutines race from here
	time.Sleep(3 * time.Second)
	close(stop)
	wg.Wait()
}
