package logger

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateWriter blocks every Write until released, standing in for a stdout pipe
// that journald has stopped draining.
type gateWriter struct {
	release chan struct{}
	mu      sync.Mutex
	buf     bytes.Buffer
}

func (g *gateWriter) Write(p []byte) (int, error) {
	<-g.release
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.Write(p)
}

func (g *gateWriter) String() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.String()
}

// A stalled output must not stall the callers: each request that logged used
// to block on the stdout write, which is how a backed-up journald turned a
// request flood into minute-long requests and an OOM kill.
func TestAsyncWriterNeverBlocksOnStalledOutput(t *testing.T) {
	g := &gateWriter{release: make(chan struct{})}
	a := newAsyncWriter(g)

	lines := asyncQueueLen * 2
	done := make(chan struct{})
	go func() {
		for i := 0; i < lines; i++ {
			_, _ = a.Write([]byte("line\n"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked while the output was stalled")
	}

	close(g.release)
	a.flush(5 * time.Second)
	// The drop report is written after the queue drains; give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(g.String(), "lines dropped") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out := g.String()
	if !strings.Contains(out, "lines dropped") {
		t.Error("dropped lines were not reported")
	}
	if n := strings.Count(out, "line\n"); n == 0 || n >= lines {
		t.Errorf("wrote %d of %d lines; want some written and the overflow dropped", n, lines)
	}
}

// Flush must deliver what was queued, so the lines logged just before exit
// are not lost.
func TestAsyncWriterFlushDeliversQueuedLines(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	a := newAsyncWriter(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	for i := 0; i < 100; i++ {
		_, _ = a.Write([]byte("x\n"))
	}
	a.flush(5 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if n := strings.Count(buf.String(), "x\n"); n != 100 {
		t.Errorf("flushed %d lines, want 100", n)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
