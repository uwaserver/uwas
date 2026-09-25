package cloudflare

import (
	"strings"
	"sync"
	"testing"
)

func TestRingBuffer_KeepsLastNLines(t *testing.T) {
	rb := newRingBuffer(3)
	rb.Write([]byte("a\nb\nc\nd\ne\n"))
	got := rb.String()
	if got != "c\nd\ne" {
		t.Errorf("got %q, want %q", got, "c\nd\ne")
	}
}

func TestRingBuffer_PartialLineCarry(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello "))
	rb.Write([]byte("world\nnext"))
	got := rb.String()
	// "hello world" is one complete line; "next" is carried.
	if !strings.Contains(got, "hello world") || !strings.Contains(got, "next") {
		t.Errorf("unexpected buffer: %q", got)
	}
}

func TestRingBuffer_ConcurrentWrites(t *testing.T) {
	rb := newRingBuffer(100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rb.Write([]byte("line\n"))
		}()
	}
	wg.Wait()
	// Just make sure we didn't race or panic; output isn't deterministic.
	if rb.String() == "" {
		t.Error("expected some buffered output")
	}
}

// maxExpectedLine pins the byte bound the ring buffer contract enforces on
// any single line — terminated or not. cloudflared child-process
// stdout/stderr drain into Write via drainOutput (tunnel.go) in 4KB chunks;
// lines are capped by count (cap), so without a per-line byte bound a child
// emitting one gigantic line (or output with no newlines at all) grows the
// buffer — and the String() output served to the dashboard — without limit.
// Deliberately independent of maxLineBytes so a bound change fails here.
const maxExpectedLine = 64 * 1024

// TestRingBufferCarryIsBounded is a regression test for unbounded
// partial-line buffering: r.carry += string(p) had no cap, so unterminated
// input grew carry without limit.
func TestRingBufferCarryIsBounded(t *testing.T) {
	r := newRingBuffer(4)

	// Single oversized unterminated write; distinctive tail so head-vs-tail
	// retention is content-distinguishable.
	payload := strings.Repeat("a", 1<<20) + "ZZZTAIL" // 1 MiB, no newline
	if n, err := r.Write([]byte(payload)); err != nil || n != len(payload) {
		t.Fatalf("Write returned n=%d err=%v", n, err)
	}
	if got := len(r.carry); got > maxExpectedLine {
		t.Fatalf("single write: carry grew to %d bytes (bound %d) — unbounded partial-line buffering", got, maxExpectedLine)
	}
	if !strings.HasSuffix(r.carry, "ZZZTAIL") {
		t.Fatalf("carry does not retain the most recent tail of the partial line")
	}

	// Accumulated unterminated writes must also be bounded — a per-write
	// guard would not catch this path.
	r2 := newRingBuffer(4)
	chunk := strings.Repeat("b", 8*1024)
	for i := 0; i < 16; i++ { // 128 KiB total, still no newline
		r2.Write([]byte(chunk))
	}
	if got := len(r2.carry); got > maxExpectedLine {
		t.Fatalf("accumulated writes: carry grew to %d bytes (bound %d)", got, maxExpectedLine)
	}
}

// TestRingBufferLineIsBounded is a regression test for the terminated-line
// manifestation of the same root cause: lines were capped by count only, so
// one gigantic line survived into lines[] at full length.
func TestRingBufferLineIsBounded(t *testing.T) {
	r := newRingBuffer(4)
	payload := strings.Repeat("c", maxExpectedLine+4096) + "ENDTAIL\n"

	r.Write([]byte(payload))

	if len(r.lines) != 1 {
		t.Fatalf("expected 1 flushed line, got %d", len(r.lines))
	}
	if got := len(r.lines[0]); got > maxExpectedLine {
		t.Fatalf("terminated line: line grew to %d bytes (bound %d) — lines capped by count only", got, maxExpectedLine)
	}
	if !strings.HasSuffix(r.lines[0], "ENDTAIL") {
		t.Fatalf("retained line does not keep the most recent tail")
	}
	if r.carry != "" {
		t.Fatalf("expected empty carry after newline, got %d bytes", len(r.carry))
	}
}

// TestRingBufferTruncatedCarryRecovers guards the recovery path: once a
// truncated carry is finally terminated by a newline, the retained tail must
// flush as a line and the ring must keep accepting input.
func TestRingBufferTruncatedCarryRecovers(t *testing.T) {
	r := newRingBuffer(4)
	r.Write([]byte(strings.Repeat("x", maxExpectedLine+4096))) // truncated tail retained
	r.Write([]byte("TAIL\nafter\n"))                            // flush + one more line

	if got := len(r.lines); got != 2 {
		t.Fatalf("expected 2 lines after flush, got %d", got)
	}
	if r.carry != "" {
		t.Fatalf("expected empty carry after flush, got %q", r.carry)
	}
	if !strings.HasSuffix(r.lines[0], "TAIL") {
		tail := r.lines[0]
		if len(tail) > 32 {
			tail = tail[len(tail)-32:]
		}
		t.Fatalf("flushed truncated line does not end with TAIL, tail=%q", tail)
	}
	if r.lines[1] != "after" {
		t.Fatalf("line after flush = %q, want %q", r.lines[1], "after")
	}
}

// TestRingBufferNormalLinesUnaffected is the control: ordinary line input
// keeps the documented last-N-lines behavior.
func TestRingBufferNormalLinesUnaffected(t *testing.T) {
	r := newRingBuffer(3)
	r.Write([]byte("a\nb\nc\nd\ne\n"))
	if got := r.String(); got != "c\nd\ne" {
		t.Fatalf("got %q, want %q", got, "c\nd\ne")
	}
}
