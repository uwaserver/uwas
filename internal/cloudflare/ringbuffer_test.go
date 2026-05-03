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
