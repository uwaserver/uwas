package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// F26600: the early-exit log tail must be read from the end of the file. The
// per-app log is opened O_APPEND and never rotated, so reading it whole put the
// entire history in memory on every failed (re)start.
func TestTailLogFileReadsOnlyTheTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	if err := f.Truncate(size); err != nil { // sparse: reads as zeros
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("old noise\nfatal: port in use\n"), size-29); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := tailLogFile(path, 4096)
	runtime.ReadMemStats(&after)

	if !strings.HasSuffix(got, "fatal: port in use") {
		t.Fatalf("tail = %q, want it to end with the last line", got)
	}
	// Control: a bound of a few lastN windows is generous; whole-file is 64 MiB.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Fatalf("tailLogFile(4096) allocated %d bytes for a %d byte log; want <= 1 MiB", alloc, size)
	}
}

func TestTailLogFileSmallAndMissing(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "s.log")
	if err := os.WriteFile(small, []byte("  hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tailLogFile(small, 4096); got != "hello" {
		t.Fatalf("small file tail = %q", got)
	}
	exact := filepath.Join(dir, "e.log")
	os.WriteFile(exact, []byte("abcdef"), 0o644)
	if got := tailLogFile(exact, 6); got != "abcdef" {
		t.Fatalf("exact-size tail = %q", got)
	}
	if got := tailLogFile(exact, 3); got != "def" {
		t.Fatalf("partial tail = %q", got)
	}
	if got := tailLogFile(filepath.Join(dir, "nope"), 10); got != "" {
		t.Fatalf("missing file tail = %q", got)
	}
	if got := tailLogFile(dir, 10); got != "" {
		t.Fatalf("directory tail = %q", got)
	}
}
