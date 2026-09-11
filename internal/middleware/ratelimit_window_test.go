package middleware

import (
	"context"
	"testing"
	"time"
)

// TestRateLimiterWindowBoundaryConsistency verifies that exactly `limit` requests
// are admitted per window, including at the exact window boundary.
//
// The bug: the window-reset check ("has elapsed time >= window?") was evaluated
// BEFORE the token-consume check ("are tokens > 0?").  This meant a request
// arriving at the exact window expiry would be admitted under the new (refilled)
// bucket, then the reset would fire — granting one extra request per window.
//
// The fix: consume the token first, then check whether the window has expired
// and should be reset for the NEXT request.
func TestRateLimiterWindowBoundaryConsistency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limit := 5
	window := 200 * time.Millisecond

	rl := NewRateLimiter(ctx, limit, window)

	// Consume exactly `limit` requests, one per 10ms, well within the window.
	for i := 0; i < limit; i++ {
		if !rl.Allow("client-1") {
			t.Fatalf("request %d/%d: unexpected rejection (tokens should still be available)", i+1, limit)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The next request must be rejected — the limit is exhausted.
	if rl.Allow("client-1") {
		t.Error("request 6/5: bucket is empty, expected rejection")
	}

	// Advance to exactly one window duration + 1ms.
	// The new window has just started; the bucket should be refilled.
	// Crucially: no request should yet have been charged against the new window.
	elapsed := time.Duration(limit) * 10 * time.Millisecond // 50ms
	time.Sleep(window - elapsed + 1*time.Millisecond)

	// The bucket is now refilled; we must be able to consume `limit` fresh requests.
	for i := 0; i < limit; i++ {
		if !rl.Allow("client-1") {
			t.Fatalf("request %d/%d after window expiry: unexpected rejection", i+1, limit)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Bucket is empty again.
	if rl.Allow("client-1") {
		t.Error("request 6/5 in second window: bucket is empty, expected rejection")
	}
}

// TestRateLimiterExactBoundaryRequests confirms that exactly `limit` requests
// are admitted when requests arrive precisely at window boundaries.
// The buggy code admitted `limit+1` requests in that situation.
func TestRateLimiterExactBoundaryRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limit := 5
	window := 100 * time.Millisecond

	rl := NewRateLimiter(ctx, limit, window)

	// Exhaust the window.
	for i := 0; i < limit; i++ {
		rl.Allow("client-1")
	}

	// Wait exactly one window.
	time.Sleep(window)

	// The next request (first of the new window) is allowed.
	if !rl.Allow("client-1") {
		t.Fatal("first request of new window should be allowed (bucket refilled)")
	}

	// Consume the remaining `limit-1` tokens.
	for i := 1; i < limit; i++ {
		rl.Allow("client-1")
	}

	// Bucket must be exhausted in the new window.
	// Bug: this would be ALLOWED (extra request from the double-reset bug).
	// Fix: this must be REJECTED.
	if rl.Allow("client-1") {
		t.Errorf("request %d/%d in new window: expected rejection (bucket exhausted), got ALLOW — "+
			"this indicates the window-boundary reset bug is still present", limit+1, limit)
	}
}
