package cloudflare

import (
	"strings"
	"sync"
)

// ringBuffer keeps the last N lines of text. Safe for concurrent writers.
// Used to expose recent cloudflared logs to the dashboard.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
	carry string // partial last line
}

func newRingBuffer(cap int) *ringBuffer {
	if cap <= 0 {
		cap = 32
	}
	return &ringBuffer{cap: cap, lines: make([]string, 0, cap)}
}

// maxLineBytes bounds how much of any single line — terminated or not — the
// ring buffer retains. Lines longer than this keep only their most recent
// tail, so a child process emitting gigantic lines (or output with no
// newlines at all) cannot grow the buffer without limit.
const maxLineBytes = 64 * 1024

// Write appends bytes, splitting on newlines, and keeps only the last cap lines.
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.carry += string(p)
	for {
		idx := strings.IndexByte(r.carry, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(r.carry[:idx], "\r")
		if len(line) > maxLineBytes {
			line = line[len(line)-maxLineBytes:]
		}
		r.carry = r.carry[idx+1:]
		r.lines = append(r.lines, line)
		if len(r.lines) > r.cap {
			r.lines = r.lines[len(r.lines)-r.cap:]
		}
	}
	// Cap the unterminated remainder the same way: complete lines have
	// already flushed above, so only a partial line is left.
	if len(r.carry) > maxLineBytes {
		r.carry = r.carry[len(r.carry)-maxLineBytes:]
	}
	return len(p), nil
}

// String returns the buffered lines joined with newlines.
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.carry != "" {
		out := append([]string{}, r.lines...)
		out = append(out, r.carry)
		return strings.Join(out, "\n")
	}
	return strings.Join(r.lines, "\n")
}
