package static

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

func fcWriteFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fcStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestFileCacheHitAndMiss(t *testing.T) {
	dir := t.TempDir()
	p := fcWriteFile(t, dir, "a.txt", "hello")
	fc := newFileCache(1<<20, 1<<20, time.Second)

	if fc.get(p, fcStat(t, p)) != nil {
		t.Fatal("empty cache returned an entry")
	}

	info := fcStat(t, p)
	fc.put(&fileEntry{path: p, body: []byte("hello"), size: info.Size(), modTime: info.ModTime()})

	e := fc.get(p, fcStat(t, p))
	if e == nil {
		t.Fatal("stored entry was not returned")
	}
	if string(e.body) != "hello" {
		t.Errorf("body = %q, want hello", e.body)
	}
}

// TestFileCacheDetectsEdit is the correctness test that matters: a file
// changed on disk must never be served from memory.
func TestFileCacheDetectsEdit(t *testing.T) {
	dir := t.TempDir()
	p := fcWriteFile(t, dir, "a.txt", "old")
	fc := newFileCache(1<<20, 1<<20, time.Second)

	info := fcStat(t, p)
	fc.put(&fileEntry{path: p, body: []byte("old"), size: info.Size(), modTime: info.ModTime()})

	// Rewrite with different content and a distinct mtime.
	if err := os.WriteFile(p, []byte("longer content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if e := fc.get(p, fcStat(t, p)); e != nil {
		t.Fatalf("edited file served from cache: %q", e.body)
	}
	if fc.Len() != 0 {
		t.Errorf("stale entry left behind, Len = %d", fc.Len())
	}
}

// TestFileCacheDetectsEditSameSize covers the case a size check alone misses.
func TestFileCacheDetectsEditSameSize(t *testing.T) {
	dir := t.TempDir()
	p := fcWriteFile(t, dir, "a.txt", "aaa")
	fc := newFileCache(1<<20, 1<<20, time.Second)

	info := fcStat(t, p)
	fc.put(&fileEntry{path: p, body: []byte("aaa"), size: info.Size(), modTime: info.ModTime()})

	if err := os.WriteFile(p, []byte("bbb"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if e := fc.get(p, fcStat(t, p)); e != nil {
		t.Fatalf("same-size edit served from cache: %q", e.body)
	}
}

// TestFileCacheRevalidatesWithoutInfo exercises the clock-based path taken
// when the caller has no fresh FileInfo to hand.
func TestFileCacheRevalidatesWithoutInfo(t *testing.T) {
	dir := t.TempDir()
	p := fcWriteFile(t, dir, "a.txt", "v1")
	fc := newFileCache(1<<20, 1<<20, 10*time.Millisecond)

	info := fcStat(t, p)
	fc.put(&fileEntry{path: p, body: []byte("v1"), size: info.Size(), modTime: info.ModTime()})

	// Inside the window: trusted with no syscall.
	if fc.get(p, nil) == nil {
		t.Fatal("entry inside the revalidate window was dropped")
	}

	if err := os.WriteFile(p, []byte("v2-longer"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	if e := fc.get(p, nil); e != nil {
		t.Fatalf("stale entry survived revalidation: %q", e.body)
	}
}

func TestFileCacheSkipsOversizeFiles(t *testing.T) {
	fc := newFileCache(10, 1<<20, time.Second)
	fc.put(&fileEntry{path: "/big", body: make([]byte, 100), size: 100})
	if fc.Len() != 0 {
		t.Errorf("oversize file was cached, Len = %d", fc.Len())
	}
}

func TestFileCacheEvictsOverBudget(t *testing.T) {
	// Budget fits ~4 entries; insert 40 and the cache must stay bounded.
	fc := newFileCache(1<<20, 400, time.Second)
	for i := 0; i < 40; i++ {
		fc.put(&fileEntry{
			path: filepath.Join("/f", string(rune('a'+i%26)), string(rune('0'+i/26))),
			body: make([]byte, 100),
			size: 100,
		})
	}
	if used := fc.used.Load(); used > 400*2 {
		t.Errorf("used = %d bytes, want bounded near the 400 byte budget", used)
	}
}

// TestFileCacheNilIsUsable keeps the no-cache handler path branch-free.
func TestFileCacheNilIsUsable(t *testing.T) {
	var fc *fileCache
	if fc.get("/x", nil) != nil {
		t.Error("nil cache returned an entry")
	}
	fc.put(&fileEntry{path: "/x"})
	if fc.Len() != 0 {
		t.Error("nil cache reported entries")
	}
}

// TestNewWithFileCacheRespectsConfig covers the operator switch: a disabled
// cache must leave the handler reading from disk every time.
func TestNewWithFileCacheRespectsConfig(t *testing.T) {
	off := false
	if h := NewWithFileCache(config.StaticFileCache{Enabled: &off}); h.files != nil {
		t.Error("cache built despite enabled: false")
	}

	h := NewWithFileCache(config.StaticFileCache{})
	if h.files == nil {
		t.Fatal("absent config should mean enabled")
	}
	if h.files.maxFileSize != defaultFileCacheMaxFile || h.files.maxBytes != defaultFileCacheMaxBytes {
		t.Errorf("zero budgets = %d/%d, want the defaults", h.files.maxFileSize, h.files.maxBytes)
	}

	h = NewWithFileCache(config.StaticFileCache{
		MaxFileSize: 4096,
		MaxBytes:    8192,
		Revalidate:  config.Duration{Duration: 5 * time.Second},
	})
	if h.files.maxFileSize != 4096 || h.files.maxBytes != 8192 || h.files.revalidate != 5*time.Second {
		t.Errorf("config not applied: %d/%d/%v", h.files.maxFileSize, h.files.maxBytes, h.files.revalidate)
	}
}
