package logger

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// asyncQueueLen bounds how many log lines may wait for the output. At ~300
// bytes a line that is a few MB at most — the cap is the point: it is what
// keeps a stalled output from turning into unbounded memory.
const asyncQueueLen = 16384

// asyncWriter decouples log callers from the output.
//
// slog writes each line to os.Stdout synchronously under a handler mutex.
// Under systemd, stdout is a pipe to journald; during a request flood journald
// falls behind (and starts suppressing), the 64 KiB pipe fills, and every
// write blocks. Every request goroutine that logs — the access line, a
// "slow request" warning, a TLS handshake error — then queues on that one
// mutex. A 301 that takes microseconds to build took minutes to finish, the
// stalled goroutines and their buffers piled up into gigabytes, and the
// process was OOM-killed: the log stream took the server down.
//
// Write never blocks: a line is queued, or dropped and counted when the queue
// is full. A single goroutine drains the queue to the output and reports the
// dropped count once the output catches up.
type asyncWriter struct {
	out     io.Writer
	ch      chan []byte
	dropped atomic.Uint64
	pending sync.WaitGroup
}

func newAsyncWriter(out io.Writer) *asyncWriter {
	a := &asyncWriter{out: out, ch: make(chan []byte, asyncQueueLen)}
	go a.run()
	return a
}

func (a *asyncWriter) Write(p []byte) (int, error) {
	// slog reuses its buffer after Write returns, so the line must be copied.
	b := make([]byte, len(p))
	copy(b, p)
	a.pending.Add(1)
	select {
	case a.ch <- b:
	default:
		a.pending.Done()
		a.dropped.Add(1)
	}
	return len(p), nil
}

func (a *asyncWriter) run() {
	// Report drops when the queue empties, and at least once a second while a
	// sustained flood keeps it from emptying.
	last := time.Now()
	for b := range a.ch {
		_, _ = a.out.Write(b)
		a.pending.Done()
		if len(a.ch) == 0 || time.Since(last) >= time.Second {
			a.reportDropped()
			last = time.Now()
		}
	}
}

func (a *asyncWriter) reportDropped() {
	if n := a.dropped.Swap(0); n > 0 {
		_, _ = fmt.Fprintf(a.out, "time=%s level=WARN msg=\"log output backlogged, lines dropped\" dropped=%d\n",
			time.Now().Format(time.RFC3339Nano), n)
	}
}

// flush waits until queued lines are written, or until timeout passes.
func (a *asyncWriter) flush(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		a.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
