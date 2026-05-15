package admin

import "sync"

// ringBuffer is a fixed-capacity in-memory ring buffer for append-only streams
// like log entries and audit records. The zero value is not usable; construct
// via newRingBuffer.
//
// Internal fields are exposed (lowercase) so tests in the same package can
// inspect them directly without going through the public API.
type ringBuffer[T any] struct {
	mu      sync.Mutex
	entries []T
	pos     int
	full    bool
	cap     int
}

func newRingBuffer[T any](capacity int) *ringBuffer[T] {
	if capacity <= 0 {
		capacity = 1
	}
	return &ringBuffer[T]{
		entries: make([]T, capacity),
		cap:     capacity,
	}
}

// Append writes one entry, advancing the write position. Wraps around on
// overflow and sets full=true once the buffer has been filled at least once.
func (r *ringBuffer[T]) Append(e T) {
	r.mu.Lock()
	r.entries[r.pos] = e
	r.pos = (r.pos + 1) % r.cap
	if r.pos == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

// Snapshot returns a copy of all entries in chronological order (oldest first).
func (r *ringBuffer[T]) Snapshot() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int
	if r.full {
		count = r.cap
	} else {
		count = r.pos
	}
	var start int
	if r.full {
		start = r.pos
	}
	out := make([]T, 0, count)
	for i := 0; i < count; i++ {
		idx := (start + i) % r.cap
		out = append(out, r.entries[idx])
	}
	return out
}

// Seed replaces the buffer contents with the given tail slice (most-recent
// last). Marks the buffer full when len(tail) >= capacity. Used by audit log
// replay on startup.
func (r *ringBuffer[T]) Seed(tail []T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var zero T
	for i := range r.entries {
		r.entries[i] = zero
	}
	copy(r.entries, tail)
	if len(tail) >= r.cap {
		r.full = true
		r.pos = 0
	} else {
		r.full = false
		r.pos = len(tail)
	}
}

// PosAndEntries returns the current write position plus a reference to the
// underlying slice for read-only streaming use. Callers must not modify the
// returned slice. Used by the SSE log stream which polls for new entries.
func (r *ringBuffer[T]) PosAndEntries() (int, []T) {
	r.mu.Lock()
	pos := r.pos
	entries := r.entries
	r.mu.Unlock()
	return pos, entries
}
