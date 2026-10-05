// Regression: configured int thresholds must not narrow to signed 32 bits.
package proxy

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestCircuitBreakerWideThresholds(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("requires 64-bit int configuration")
	}
	wide := int64(1) << 31
	control := NewCircuitBreaker(2, time.Hour)
	control.RecordFailure()
	if control.State() != CircuitClosed {
		t.Fatal("ordinary threshold")
	}
	control.RecordFailure()
	if control.State() != CircuitOpen {
		t.Fatal("ordinary trip")
	}
	cb := NewCircuitBreaker(int(wide), time.Hour)
	cb.RecordFailure()
	if cb.State() != CircuitClosed || !cb.Allow() {
		t.Fatal("large threshold prematurely opened")
	}
	for _, threshold := range []int64{wide - 1, wide, wide + 1} {
		cb := NewCircuitBreaker(int(threshold), time.Hour)
		cb.failures.Store(threshold - 2)
		cb.RecordFailure()
		if cb.State() != CircuitClosed || cb.failures.Load() != threshold-1 {
			t.Fatalf("threshold=%d early open/count=%d", threshold, cb.failures.Load())
		}
		cb.RecordFailure()
		if cb.State() != CircuitOpen || cb.failures.Load() != threshold || cb.Allow() {
			t.Fatalf("threshold=%d exact trip failed", threshold)
		}
		cb.lastFailure.Store(0)
		start := make(chan struct{})
		out := make(chan bool, 32)
		var ready sync.WaitGroup
		ready.Add(32)
		for i := 0; i < 32; i++ {
			go func() { ready.Done(); <-start; out <- cb.Allow() }()
		}
		ready.Wait()
		close(start)
		allowed := 0
		for i := 0; i < 32; i++ {
			if <-out {
				allowed++
			}
		}
		if allowed != 1 || cb.State() != CircuitHalfOpen {
			t.Fatalf("admitted probes=%d", allowed)
		}
		cb.RecordFailure()
		if cb.State() != CircuitOpen || cb.probeSlot.Load() != 0 {
			t.Fatal("failed probe")
		}
		cb.lastFailure.Store(0)
		if !cb.Allow() {
			t.Fatal("second probe")
		}
		cb.RecordSuccess()
		if cb.State() != CircuitClosed || cb.failures.Load() != 0 || cb.probeSlot.Load() != 0 {
			t.Fatal("recovery reset")
		}
		cb.RecordFailure()
		cb.RecordSuccess()
		if cb.failures.Load() != 0 || !cb.Allow() {
			t.Fatal("closed-state reset")
		}
	}
	for _, threshold := range []int{0, -1} {
		cb := NewCircuitBreaker(threshold, 0)
		if cb.threshold != 5 || cb.timeout != 30*time.Second {
			t.Fatal("defaults")
		}
	}
	fmt.Println("FIX VERIFIED")
}
