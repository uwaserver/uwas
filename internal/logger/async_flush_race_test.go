package logger

import (
	"io"
	"sync"
	"testing"
	"time"
)

// flush ran sync.WaitGroup.Wait while Write kept calling Add, so a shutdown
// that logged concurrently panicked in the flush goroutine with "WaitGroup is
// reused before previous Wait has returned" and took the process down (F54001).
func TestAsyncWriterFlushWhileWriting(t *testing.T) {
	a := newAsyncWriter(io.Discard)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					a.Write([]byte("x\n"))
				}
			}
		}()
	}
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		a.flush(50 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

type flushGateWriter struct {
	gate chan struct{}
	mu   sync.Mutex
	n    int
}

func (g *flushGateWriter) Write(p []byte) (int, error) {
	<-g.gate
	g.mu.Lock()
	g.n++
	g.mu.Unlock()
	return len(p), nil
}

func TestAsyncWriterFlushWaitsForQueuedLines(t *testing.T) {
	g := &flushGateWriter{gate: make(chan struct{})}
	a := newAsyncWriter(g)
	a.Write([]byte("one\n"))
	a.Write([]byte("two\n"))

	start := time.Now()
	a.flush(30 * time.Millisecond)
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("flush returned before the output accepted the queued lines")
	}

	close(g.gate)
	a.flush(5 * time.Second)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n != 2 {
		t.Fatalf("written lines = %d, want 2 after flush", g.n)
	}
}

func TestAsyncWriterFlushEmptyReturnsImmediately(t *testing.T) {
	a := newAsyncWriter(io.Discard)
	start := time.Now()
	a.flush(5 * time.Second)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("flush of an idle writer took %s", d)
	}
}
