package autoblock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A block made just before shutdown has its save request still queued when
// the context is cancelled. The shutdown flush must take that request
// instead of letting select pick Done with dirty=false (F2230).
func TestSaveWorkerFlushesQueuedRequestOnShutdown(t *testing.T) {
	for i := 0; i < 200; i++ {
		path := filepath.Join(t.TempDir(), "autoblock.json")
		b := New(Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}, testLogger())
		if err := b.Block("203.0.113.90", ReasonConnFlood, time.Hour); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b.saveWorker(ctx)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("round %d: queued save lost on shutdown: %v", i, err)
		}
		if !New(Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}, testLogger()).BlockedAddr("203.0.113.90") {
			t.Fatalf("round %d: block did not survive restart", i)
		}
	}
}

// Nothing queued and nothing dirty: shutdown must not write a state file.
func TestSaveWorkerShutdownWithoutChangesWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autoblock.json")
	b := New(Config{Enabled: true, BlockDuration: time.Hour, PersistPath: path}, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.saveWorker(ctx)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("state file written although nothing changed")
	}
}
