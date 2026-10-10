package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// F1270: a Set killed between CreateTemp and Rename leaves a ".uwas-cache-*"
// file that must not outlive the next start or TTL sweep.
func TestDiskCacheRemovesOrphanTempFiles(t *testing.T) {
	base := filepath.Join(t.TempDir(), "cache")
	dc := NewDiskCache(base, 1<<20)
	key := "k1"
	if err := dc.Set(key, &CachedResponse{StatusCode: 200, Body: []byte("hello"), TTL: time.Hour, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(dc.path(key))
	plant := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, 4096), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	// Sweep path: the live entry and an unrelated dotfile survive, the orphan goes.
	orphan := plant(".uwas-cache-sweep")
	other := plant(".keep-me")
	dc.cleanExpired()
	if exists(orphan) {
		t.Error("sweep left the orphan temp file")
	}
	if !exists(other) {
		t.Error("sweep removed an unrelated file")
	}
	if _, err := dc.Get(key); err != nil {
		t.Errorf("live entry lost by sweep: %v", err)
	}

	// Startup path.
	orphan = plant(".uwas-cache-start")
	dc2 := NewDiskCache(base, 1<<20)
	if exists(orphan) {
		t.Error("startup left the orphan temp file")
	}
	if _, err := dc2.Get(key); err != nil {
		t.Errorf("live entry lost at startup: %v", err)
	}
	if got, want := dc2.usedBytes.Load(), int64(len(mustSerialize(t, dc2, key))); got != want {
		t.Errorf("usedBytes=%d want %d", got, want)
	}
}

func mustSerialize(t *testing.T, dc *DiskCache, key string) []byte {
	t.Helper()
	b, err := os.ReadFile(dc.path(key))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
