//go:build unix

package autoblock

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Start must not return while the final state save is still running, or a
// caller (the server's shutdown path) cannot tell when the state is flushed
// (F2261). The save is held at a FIFO standing in for a slow disk.
func TestStartJoinsSaveWorker(t *testing.T) {
	t.Run("control: unheld save is done when Start returns", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "autoblock.json")
		b := New(Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}, testLogger())
		if err := b.Block("203.0.113.91", ReasonConnFlood, time.Hour); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b.Start(ctx)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("state not flushed when Start returned: %v", err)
		}
	})

	t.Run("Start waits for a held save", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "autoblock.json")
		if err := syscall.Mkfifo(path+".tmp", 0o600); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
		b := New(Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}, testLogger())
		if err := b.Block("203.0.113.92", ReasonConnFlood, time.Hour); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan struct{})
		go func() { b.Start(ctx); close(done) }()

		// The save cannot finish until the FIFO has a reader, so Start
		// returning here means it did not join the worker.
		select {
		case <-done:
			t.Fatal("Start returned while the final save was still blocked")
		case <-time.After(200 * time.Millisecond):
		}

		r, err := os.OpenFile(path+".tmp", os.O_RDONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		go io.Copy(io.Discard, r)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not return after the save was released")
		}
	})
}
