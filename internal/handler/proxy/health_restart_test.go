package proxy

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestHealthCheckerCanRestartAfterParentContextEnds(t *testing.T) {
	hc := NewHealthChecker(NewUpstreamPool(nil), HealthConfig{Interval: time.Hour}, nil)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		hc.Start(ctx)
		cancel()

		deadline := time.Now().Add(3 * time.Second)
		for {
			hc.mu.Lock()
			released := hc.cancel == nil
			hc.mu.Unlock()
			if released {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %d retained its restart guard after parent cancellation", i+1)
			}
			runtime.Gosched()
		}
	}
}
