package deploy

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

type countingSink struct {
	b     bytes.Buffer
	total int
}

func (s *countingSink) WriteString(x string) (int, error) {
	s.total += len(x)
	return s.b.WriteString(x)
}
func (s *countingSink) Write(p []byte) (int, error) { s.total += len(p); return s.b.Write(p) }
func (s *countingSink) String() string              { return s.b.String() }

// F56000: a build step runs tenant repo code, and its whole output used to be
// buffered in memory before reaching the log; a chatty or runaway script could
// exhaust the server's memory. The log keeps a bounded tail instead.
func TestRunShellBoundsStepOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	var sink countingSink
	const emit = 16 << 20
	cmd := "head -c 16777216 /dev/zero | tr '\\000' x; echo END-MARKER"
	if err := runShell(context.Background(), t.TempDir(), cmd, &sink, nil); err != nil {
		t.Fatal(err)
	}
	if sink.total > maxStepOutput+4096 {
		t.Fatalf("step output not bounded: %d bytes reached the log (emitted %d), cap %d", sink.total, emit, maxStepOutput)
	}
	out := sink.String()
	if !strings.Contains(out, "END-MARKER") {
		t.Error("the tail of the output (where a build failure shows) was dropped")
	}
	if !strings.Contains(out, "truncated") {
		t.Error("truncation is not announced in the log")
	}
}

// Control: a small step output is logged verbatim.
func TestRunShellSmallOutputVerbatim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	var sink countingSink
	if err := runShell(context.Background(), t.TempDir(), "echo hello; echo oops 1>&2", &sink, nil); err != nil {
		t.Fatal(err)
	}
	if out := sink.String(); !strings.Contains(out, "hello") || !strings.Contains(out, "oops") || strings.Contains(out, "truncated") {
		t.Fatalf("unexpected log %q", out)
	}
}

// F56001: on the deploy deadline only `sh` was killed; a child it had left
// running kept the output pipe open and cmd.Run waited for it, so the deploy
// (and the per-app lock behind it, queueing every webhook) hung well past the
// deadline.
func TestRunShellDeadlineReapsLeftoverChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var sink countingSink
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- runShell(ctx, t.TempDir(), "sleep 20 & sleep 20", &sink, nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a command killed by the deadline reported success")
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("returned after %s", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runShell still blocked 10s after a 300ms deadline: leftover child holds the output pipe")
	}
}

func TestTailBufferBoundaries(t *testing.T) {
	tb := &tailBuffer{max: 10}
	tb.Write([]byte("0123456789"))
	if got := string(tb.Bytes()); got != "0123456789" {
		t.Fatalf("exactly max bytes must be kept verbatim, got %q", got)
	}
	tb = &tailBuffer{max: 10}
	tb.Write([]byte("0123456789A"))
	got := string(tb.Bytes())
	if !strings.HasSuffix(got, "123456789A") || !strings.HasPrefix(got, truncatedNote) {
		t.Fatalf("one byte over max: got %q", got)
	}
	tb = &tailBuffer{max: 10}
	for i := 0; i < 100; i++ {
		tb.Write([]byte("ab"))
	}
	if got := string(tb.Bytes()); !strings.HasSuffix(got, "ababababab") || len(got) != len(truncatedNote)+10 {
		t.Fatalf("repeated writes: got %q", got)
	}
}
