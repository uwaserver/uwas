package static

import (
	"os"
	"testing"
	"time"
)

type fileCacheIdentityInfo struct {
	size             int64
	mtime            time.Time
	entered, release chan struct{}
}

func (i fileCacheIdentityInfo) Name() string { return "file" }
func (i fileCacheIdentityInfo) Size() int64 {
	if i.entered != nil {
		close(i.entered)
		<-i.release
	}
	return i.size
}
func (i fileCacheIdentityInfo) Mode() os.FileMode  { return 0644 }
func (i fileCacheIdentityInfo) ModTime() time.Time { return i.mtime }
func (i fileCacheIdentityInfo) IsDir() bool        { return false }
func (i fileCacheIdentityInfo) Sys() any           { return nil }
func fileCacheIdentityWait(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("gate timed out")
	}
}
func fileCacheIdentityCase(t *testing.T, replace bool) bool {
	t.Helper()
	fc := newFileCache(100, 100, time.Hour)
	stamp := time.Unix(1, 0)
	old := &fileEntry{path: "file", body: []byte("old"), size: 3, modTime: stamp}
	fc.put(old)
	i := fileCacheIdentityInfo{size: 4, mtime: stamp, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	var got *fileEntry
	go func() { got = fc.get("file", i); close(done) }()
	fileCacheIdentityWait(t, i.entered)
	var newer *fileEntry
	if replace {
		newer = &fileEntry{path: "file", body: []byte("new!"), size: 4, modTime: stamp}
		fc.put(newer)
	}
	close(i.release)
	fileCacheIdentityWait(t, done)
	if got != nil {
		t.Fatal("invalid old entry returned")
	}
	if !replace {
		return fc.Len() == 0 && fc.used.Load() == 0
	}
	return fc.get("file", fileCacheIdentityInfo{size: 4, mtime: stamp}) == newer && fc.Len() == 1 && fc.used.Load() == 4
}
func TestFileCacheRevalidationPreservesReplacement(t *testing.T) {
	t.Run("ordinary invalidation", func(t *testing.T) {
		if !fileCacheIdentityCase(t, false) {
			t.Fatal("stale entry retained")
		}
	})
	t.Run("gated replacement", func(t *testing.T) {
		if !fileCacheIdentityCase(t, true) {
			t.Fatal("replacement lost")
		}
	})
	t.Run("missing and matching entry", func(t *testing.T) {
		fc := newFileCache(100, 100, time.Hour)
		stamp := time.Unix(1, 0)
		e := &fileEntry{path: "file", size: 3, modTime: stamp}
		if fc.get("file", nil) != nil {
			t.Fatal("empty hit")
		}
		fc.put(e)
		if fc.get("file", fileCacheIdentityInfo{size: 3, mtime: stamp}) != e {
			t.Fatal("matching entry lost")
		}
		if fc.used.Load() != 3 {
			t.Fatal("bad accounting")
		}
	})
	t.Run("mtime mismatch", func(t *testing.T) {
		fc := newFileCache(100, 100, time.Hour)
		stamp := time.Unix(1, 0)
		fc.put(&fileEntry{path: "file", size: 3, modTime: stamp})
		if fc.get("file", fileCacheIdentityInfo{size: 3, mtime: stamp.Add(time.Second)}) != nil || fc.Len() != 0 || fc.used.Load() != 0 {
			t.Fatal("mtime invalidation failed")
		}
	})
}
