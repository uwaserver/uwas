//go:build unix

package filemanager

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestListFIFODoesNotBlock: List of a FIFO opened it with a blocking open(2)
// that waited for a writer forever, pinning the request and an OS thread; a
// domain user could repeat it until the process hit its thread limit (F67600).
func TestListFIFODoesNotBlock(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(base, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip(err)
	}

	done := make(chan error, 1)
	go func() { _, err := List(base, "pipe"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("List(fifo) succeeded; want a not-a-directory error")
		}
	case <-time.After(10 * time.Second):
		// Release the blocked open so the goroutine does not outlive the test.
		if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			w.Close()
		}
		<-done
		t.Fatal("List(fifo) blocked in open(2) waiting for a writer")
	}

	if ents, err := List(base, "."); err != nil || len(ents) != 2 {
		t.Fatalf("List(.) = %d entries, %v; want 2, nil", len(ents), err)
	}
}
