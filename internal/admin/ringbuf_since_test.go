package admin

import "testing"

// TestRingBufferSince verifies Since returns new entries and, critically,
// stays correct across write-position wraparound (the SSE log stream
// previously spun forever once the buffer wrapped).
func TestRingBufferSince(t *testing.T) {
	rb := newRingBuffer[int](4)
	pos, _ := rb.PosAndEntries()

	rb.Append(1)
	rb.Append(2)
	pos, got := rb.Since(pos)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("Since after 2 appends = %v", got)
	}

	// No new entries → nil, same position.
	pos2, got := rb.Since(pos)
	if len(got) != 0 || pos2 != pos {
		t.Fatalf("Since with no new entries = %v (pos %d→%d)", got, pos, pos2)
	}

	// Wrap the buffer: 3 more appends push pos past the capacity boundary.
	rb.Append(3)
	rb.Append(4)
	rb.Append(5) // overwrites 1; pos wraps below the consumer's position
	pos, got = rb.Since(pos)
	if len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("Since across wraparound = %v", got)
	}
	if pos < 0 || pos >= 4 {
		t.Fatalf("returned position %d out of range [0,4)", pos)
	}

	// Out-of-range consumer position must not loop or panic.
	if _, got := rb.Since(99); got != nil {
		t.Fatalf("Since(out-of-range) = %v, want nil", got)
	}
}
