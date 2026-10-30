package middleware

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestConvertImageHungConverterReleasesGlobalLock pins that a converter that
// never finishes (a pathological image a tenant uploaded) is killed after a
// bound: convertImageReal holds convertMu for the whole run, so an unbounded
// converter would stall every tenant's on-the-fly conversions (F57300).
func TestConvertImageHungConverterReleasesGlobalLock(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dst := src + ".webp"
	if err := os.WriteFile(src, []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(dir, "gate.fifo")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	// The converter blocks on the gate until the test opens it.
	origTimeout := convertTimeout
	t.Cleanup(func() { convertTimeout = origTimeout })
	convertTimeout = 300 * time.Millisecond

	origLook, origExec := convertLookPathFn, convertExecFn
	t.Cleanup(func() { convertLookPathFn, convertExecFn = origLook, origExec })
	convertLookPathFn = func(string) (string, error) { return "/bin/sh", nil }
	convertExecFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "cat "+gate+" >/dev/null")
	}

	done := make(chan bool, 1)
	go func() { done <- convertImageReal(src, dst, "webp") }()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("a converter that never produced output reported success")
		}
	case <-time.After(3 * time.Second):
		// Release the parked converter so the goroutine and lock do not leak.
		if f, err := os.OpenFile(gate, os.O_WRONLY, 0); err == nil {
			f.Close()
		}
		<-done
		t.Fatal("convertImageReal did not return: a hung converter holds convertMu indefinitely")
	}

	// Control: the lock is free again, a following conversion can start.
	if !convertMu.TryLock() {
		t.Fatal("convertMu still held after the converter was abandoned")
	}
	convertMu.Unlock()
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("no variant should be published for a failed conversion")
	}
}
