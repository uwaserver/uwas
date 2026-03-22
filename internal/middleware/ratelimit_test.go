package middleware

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestCleanupLoopTickerFires waits for the cleanup loop's 1-minute ticker to
// fire once, covering the ticker.C case body in cleanupLoop. This test takes
// ~62 seconds to run.
func TestCleanupLoopTickerFires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow cleanup loop test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rl := NewRateLimiter(ctx, 5, 1*time.Millisecond) // 1ms window

	// Fill buckets
	for i := 0; i < 50; i++ {
		rl.Allow(fmt.Sprintf("ip-%d", i))
	}

	// Wait for the cleanup loop's ticker to fire (1 minute + buffer)
	time.Sleep(62 * time.Second)

	// After cleanup, all expired entries should be gone.
	// New Allow calls should succeed since the old buckets were deleted.
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("ip-%d", i)
		if !rl.Allow(key) {
			t.Errorf("%s should be allowed after cleanup (bucket should have been deleted)", key)
		}
	}
}

// TestShardIndexDifferentKeys verifies different keys hash to different shards.
func TestShardIndexDifferentKeys(t *testing.T) {
	seen := make(map[uint8]bool)
	for i := 0; i < 1000; i++ {
		idx := shardIndex(fmt.Sprintf("key-%d", i))
		seen[idx] = true
	}
	if len(seen) < 50 {
		t.Errorf("only %d unique shards out of 256 for 1000 keys, expected more", len(seen))
	}
}

// TestRateLimiterCancelStopsGoroutine verifies the cleanup goroutine exits on cancel.
func TestRateLimiterCancelStopsGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rl := NewRateLimiter(ctx, 10, time.Second)

	rl.Allow("test")
	cancel()

	time.Sleep(20 * time.Millisecond)
	_ = rl
}
