package fastcgi

// Regression: Client.Execute's response read loop must bound how much it
// buffers from the FastCGI peer. Before the cap, every STDOUT/STDERR record
// was appended to unbounded bytes.Buffers until FCGI_END_REQUEST — a peer
// streaming stdout records without end (misdirected or hostile upstream on
// the PHP request path, internal/handler/fastcgi/handler.go) was bounded
// only by the 60s socket deadline: on a fast local socket that is tens of
// GB, i.e. fatal OOM. Execute now aborts with ErrResponseTooLarge once the
// buffered response crosses PoolConfig.MaxResponseBytes (default 64 MiB).

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// streamAndCount drains the client's request records, then streams nChunks
// max-size STDOUT records followed by END_REQUEST. It reports the number of
// stdout content bytes successfully delivered to the client.
func streamAndCount(ln net.Listener, nChunks int) int64 {
	c, err := ln.Accept()
	if err != nil {
		return 0
	}
	defer c.Close()

	br := bufio.NewReader(c)
	chunk := make([]byte, maxContentLength)
	var written int64
	requestID := uint16(1)

	// Drain begin/params/stdin until the empty STDIN terminator.
	for {
		rec, err := ReadRecord(br)
		if err != nil {
			return written
		}
		if rec.Type == TypeStdin && rec.ContentLength == 0 {
			break
		}
	}

	// Stream stdout. Write directly to the conn so each record is actually
	// pushed (and backpressure applies); stop counting on first failure —
	// that is the client tearing the connection down at its limit.
	endBody := make([]byte, 8)
	for i := 0; i < nChunks; i++ {
		if err := WriteRecord(c, TypeStdout, requestID, chunk); err != nil {
			return written
		}
		written += int64(len(chunk))
	}
	// Well-formed terminator so an unbounded client would succeed cleanly.
	if err := WriteRecord(c, TypeEndRequest, requestID, endBody); err != nil {
		return written
	}
	return written
}

// Control: a small well-formed response round-trips through Execute.
func TestExecuteSmallResponseControl(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		streamAndCount(ln, 1)
	}()

	client := NewClient(PoolConfig{Address: "tcp:" + ln.Addr().String()})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.Execute(ctx, map[string]string{"SCRIPT_NAME": "/index.php"}, nil)
	if err != nil {
		t.Fatalf("FAIL control: Execute on a small well-formed response errored: %v", err)
	}
	if len(resp.Stdout()) != maxContentLength {
		t.Fatalf("FAIL control: got %d stdout bytes, want %d", len(resp.Stdout()), maxContentLength)
	}
	wg.Wait()
}

// Default cap: the client must not consume an entire unbounded stdout
// stream. Bounded consumption threshold: 88 MiB (the bounded client stops
// at the 64 MiB default cap plus a small in-flight window; 128 MiB is the
// full hostile stream).
func TestExecuteResponseBufferingIsBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	const nChunks = 2048 // 2048 × 65535 B ≈ 128 MiB
	var written int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		written = streamAndCount(ln, nChunks)
	}()

	client := NewClient(PoolConfig{Address: "tcp:" + ln.Addr().String()})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _ = client.Execute(ctx, map[string]string{"SCRIPT_NAME": "/index.php"}, nil)
	wg.Wait()

	const bound = 88 << 20 // 88 MiB
	if written > bound {
		t.Fatalf("FAIL: client consumed %d bytes (%.0f MiB) of a hostile stdout stream — bound %d MiB — Execute buffers the entire response with no size limit", written, float64(written)/(1<<20), bound>>20)
	}
}

// Configurable cap: a pool with a small MaxResponseBytes aborts with
// ErrResponseTooLarge instead of draining the stream.
func TestExecuteResponseLimitConfigurable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	const nChunks = 48 // 48 × 65535 B ≈ 3 MiB, against a 1 MiB cap
	var written int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		written = streamAndCount(ln, nChunks)
	}()

	client := NewClient(PoolConfig{
		Address:          "tcp:" + ln.Addr().String(),
		MaxResponseBytes: 1 << 20, // 1 MiB
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = client.Execute(ctx, map[string]string{"SCRIPT_NAME": "/index.php"}, nil)
	wg.Wait()

	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("FAIL: expected ErrResponseTooLarge from a 3 MiB response against a 1 MiB cap, got: %v", err)
	}
	// Server-side byte count is deliberately NOT asserted at this scale: a
	// 3 MiB stream fits inside the loopback kernel socket buffers (~6 MiB
	// receive autotuning), so `written` reflects kernel absorption, not
	// application consumption — the client did abort at the cap (asserted
	// above). Bounded consumption is covered by the 128 MiB default-cap
	// test, whose stream dwarfs any kernel buffering.
	t.Logf("server pushed %d bytes; client aborted at the 1 MiB cap (kernel buffers absorbed the rest)", written)
}
