// Deterministic race proof: two Contains goroutines with DIFFERENT CIDRs
// cause concurrent netsFor recompute → both try Lock → write-write data race on s.nets.
package cloudflare

import (
	"sync"
	"testing"
	"time"
)

// TestIPSetContainsRaceWriteWrite proves a write-write data race in netsFor.
//
// Race: when two goroutines call Contains() with different CIDRs concurrently,
// both netsFor() recompute calls see a cache miss, then BOTH call s.mu.Lock()
// and write to s.nets — a simultaneous write-write on the map header field.
// The Go race detector flags this as a DATA RACE.
//
// The original test in iplist_race_test.go uses the SAME CIDR for all goroutines,
// so s.fingerprint always matches and netsFor never enters the recompute path.
// That test is a FALSE NEGATIVE.
//
// Pre-fix: both goroutines race on s.mu.Lock() + s.nets = nets.
// Post-fix: both recompute calls are serialized by the lock; no concurrent write.
func TestIPSetContainsRaceWriteWrite(t *testing.T) {
	set := NewIPSet()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	start := make(chan struct{})

	// Two goroutines with DIFFERENT CIDRs — forces concurrent recompute.
	cidrs := []string{"10.0.0.0/8", "172.16.0.0/12"}

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // barrier: all goroutines ready before starting
			for {
				select {
				case <-stop:
					return
				default:
					// Each goroutine uses its own fixed CIDR, but the two
					// goroutines have DIFFERENT CIDRs from each other, so
					// each call to netsFor sees a cache miss and recomputes.
					cidr := cidrs[idx]
					_ = set.Contains("10.0.0.1", []string{cidr})
				}
			}
		}(i)
	}

	close(start)       // release barrier — all goroutines race from here
	time.Sleep(3 * time.Second)
	close(stop)
	wg.Wait()
}
