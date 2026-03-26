package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

func testLogger() *logger.Logger {
	return logger.New("error", "text")
}

// TestDiskSetMkdirAllError covers disk.Set when MkdirAll fails (e.g., path
// is inside a file, not a directory).
func TestDiskSetMkdirAllError(t *testing.T) {
	dir := t.TempDir()
	// Create a file where a directory would be needed
	blockingFile := filepath.Join(dir, "block")
	os.WriteFile(blockingFile, []byte("I'm a file"), 0644)

	// Use the file as a cache base dir so that MkdirAll fails when trying
	// to create subdirectories inside it.
	dc := NewDiskCache(blockingFile, 1<<30)

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("hello"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	err := dc.Set("test-key", resp)
	if err == nil {
		t.Error("expected error from MkdirAll when base path is a file")
	}
}

// TestDiskSetOverLimit covers disk.Set when the disk limit is exceeded
// and the write is silently skipped (line 58-59).
func TestDiskSetOverLimit(t *testing.T) {
	dir := t.TempDir()
	// Create a disk cache with a very small limit
	dc := NewDiskCache(dir, 10) // 10 bytes limit

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       make([]byte, 1000), // Much larger than the limit
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	err := dc.Set("big-key", resp)
	if err != nil {
		t.Errorf("expected nil error (silently skipped), got %v", err)
	}

	// Verify nothing was written
	_, getErr := dc.Get("big-key")
	if getErr == nil {
		t.Error("expected miss after skipped write")
	}
}

// TestEngineSetWithDisk covers engine.Set when disk is configured, which
// triggers the async disk write goroutine (lines 70-76).
func TestEngineSetWithDisk(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e := NewEngine(ctx, 1<<20, dir, 1<<30, testLogger())

	req := httptest.NewRequest("GET", "http://example.com/page", nil)

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("<h1>Hello</h1>"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
		GraceTTL:   1 * time.Minute,
	}

	e.Set(req, resp)

	// Wait for async disk write
	time.Sleep(100 * time.Millisecond)

	// Verify it can be read back from memory
	got, status := e.Get(req)
	if got == nil {
		t.Fatal("expected hit from cache")
	}
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
}

// TestEngineSetDiskWriteFailure covers the disk write error log path
// (line 73) by using a disk cache that will fail to write.
func TestEngineSetDiskWriteFailure(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create engine with disk path, then break the disk path
	e := NewEngine(ctx, 1<<20, dir, 1<<30, testLogger())

	// Remove the disk directory to cause write failures
	os.RemoveAll(dir)
	// Create a file at the same path to prevent directory creation
	os.WriteFile(dir, []byte("block"), 0644)

	req := httptest.NewRequest("GET", "http://example.com/page-fail", nil)
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("data"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	// This triggers the async disk write which will fail and log a warning.
	e.Set(req, resp)
	time.Sleep(100 * time.Millisecond)
}

// TestEvictLRUNilBack covers evictLRU when the LRU list back element is nil
// (line 181-182 in memory.go). This happens when the shard's LRU is empty.
func TestEvictLRUNilBack(t *testing.T) {
	mc := NewMemoryCache(100) // very small limit

	// Create a large entry that forces eviction
	resp := &CachedResponse{
		Body:    make([]byte, 200), // larger than maxBytes
		Created: time.Now(),
		TTL:     1 * time.Minute,
	}

	// Set should try to evict from the shard, but the shard is empty,
	// so evictLRU hits the nil check and returns.
	// Then the "still over limit" check (line 100) triggers and skips storing.
	mc.Set("too-big", resp)

	// Verify it wasn't stored (over limit, nothing to evict)
	_, status := mc.Get("too-big")
	if status != StatusMiss {
		t.Errorf("expected MISS for oversized entry, got %s", status)
	}
}

// TestEngineGetPromoteDiskStale covers the disk->memory promotion path
// when a disk entry is stale (line 56 in engine.go).
func TestEngineGetPromoteDiskStale(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e := NewEngine(ctx, 1<<20, dir, 1<<30, testLogger())

	// Write a response directly to disk that is stale (past TTL but within grace)
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("stale content"),
		Created:    time.Now().Add(-2 * time.Second),
		TTL:        1 * time.Second,
		GraceTTL:   1 * time.Hour,
	}

	req := httptest.NewRequest("GET", "http://example.com/stale-page", nil)
	key := GenerateKey(req, []string{"Accept-Encoding"})

	// Write directly to disk (bypass memory)
	if err := e.disk.Set(key, resp); err != nil {
		t.Fatalf("disk.Set: %v", err)
	}

	// Get should find it on disk as stale and promote to memory
	got, status := e.Get(req)
	if got == nil {
		t.Fatal("expected stale entry from disk")
	}
	if status != StatusStale {
		t.Errorf("status = %q, want STALE", status)
	}
}

// TestEngineGetPromoteDiskFresh covers the disk->memory promotion when fresh.
func TestEngineGetPromoteDiskFresh(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e := NewEngine(ctx, 1<<20, dir, 1<<30, testLogger())

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("fresh disk content"),
		Created:    time.Now(),
		TTL:        1 * time.Hour,
		GraceTTL:   1 * time.Hour,
	}

	req := httptest.NewRequest("GET", "http://example.com/fresh-disk", nil)
	key := GenerateKey(req, []string{"Accept-Encoding"})

	// Write directly to disk
	if err := e.disk.Set(key, resp); err != nil {
		t.Fatalf("disk.Set: %v", err)
	}

	got, status := e.Get(req)
	if got == nil {
		t.Fatal("expected fresh entry from disk")
	}
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
}

// TestDiskSetWriteFileError covers disk.Set when WriteFile fails (line 68-69).
func TestDiskSetWriteFileError(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<30)

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("test"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	// Get the path that would be written
	key := "test-write-error"
	path := dc.path(key)
	pathDir := filepath.Dir(path)

	// Create the directory structure, then place a directory where the file
	// should go, causing WriteFile to fail.
	os.MkdirAll(pathDir, 0755)
	os.MkdirAll(path, 0755) // path is now a directory, not a file

	err := dc.Set(key, resp)
	if err == nil {
		t.Error("expected error when WriteFile target is a directory")
	}
}
