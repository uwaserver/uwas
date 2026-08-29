package static

import (
	"container/list"
	"hash/fnv"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// fileCache keeps small static files in memory alongside their metadata, so a
// repeatedly requested file is served without touching the filesystem.
//
// The profile that motivated it: on the uncached static path roughly 87% of a
// request was filesystem syscalls — os.Open 65%, os.Stat 16%, then Seek, Read
// and Close. None of that work changes between two requests for a file that
// has not been edited.
//
// This sits below the response cache in internal/cache, not beside it. That
// one stores a rendered response per domain and can be turned off per site;
// this one stores file bytes and helps every static handler path, including
// pre-compressed variants and domains that deliberately cache nothing.
//
// Staleness is bounded by revalidateAfter: once an entry is older than that,
// a single Stat confirms size and mtime before the bytes are reused, and a
// changed file is dropped. Inside that window a hit costs no syscall at all.
type fileCache struct {
	maxFileSize int64
	maxBytes    int64
	used        atomic.Int64
	revalidate  time.Duration
	shards      [fileCacheShards]fileShard
}

const fileCacheShards = 64

type fileShard struct {
	mu    sync.Mutex
	items map[string]*list.Element
	lru   *list.List
}

// fileEntry is one cached file. Everything here is immutable after insert
// except checkedAt, so readers only need the shard lock long enough to take
// the pointer.
type fileEntry struct {
	path        string
	body        []byte
	size        int64
	modTime     time.Time
	etag        string
	contentType string

	// checkedAt is the unix-nano time of the last successful revalidation.
	// Atomic because several requests can decide to revalidate at once and
	// the loser's write is harmless.
	checkedAt atomic.Int64
}

func newFileCache(maxFileSize, maxBytes int64, revalidate time.Duration) *fileCache {
	fc := &fileCache{maxFileSize: maxFileSize, maxBytes: maxBytes, revalidate: revalidate}
	for i := range fc.shards {
		fc.shards[i].items = make(map[string]*list.Element)
		fc.shards[i].lru = list.New()
	}
	return fc
}

func (fc *fileCache) shard(path string) *fileShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	return &fc.shards[h.Sum32()%fileCacheShards]
}

// get returns a validated entry, or nil when the caller should read the file
// itself. A nil fileCache is a working no-op so the handler needs no branch.
func (fc *fileCache) get(path string, info os.FileInfo) *fileEntry {
	if fc == nil {
		return nil
	}
	sh := fc.shard(path)

	sh.mu.Lock()
	el, ok := sh.items[path]
	if ok {
		sh.lru.MoveToFront(el)
	}
	sh.mu.Unlock()
	if !ok {
		return nil
	}
	e := el.Value.(*fileEntry)

	// The caller often already holds a fresh FileInfo from ResolveRequest. When
	// it does, trust it over the clock: it is strictly better evidence.
	if info != nil {
		if info.Size() != e.size || !info.ModTime().Equal(e.modTime) {
			fc.remove(path)
			return nil
		}
		e.checkedAt.Store(time.Now().UnixNano())
		return e
	}

	if time.Since(time.Unix(0, e.checkedAt.Load())) < fc.revalidate {
		return e
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != e.size || !st.ModTime().Equal(e.modTime) {
		fc.remove(path)
		return nil
	}
	e.checkedAt.Store(time.Now().UnixNano())
	return e
}

// put stores a file. Oversized files are skipped rather than evicting the
// working set to hold one big object.
func (fc *fileCache) put(e *fileEntry) {
	if fc == nil || e.size > fc.maxFileSize {
		return
	}
	e.checkedAt.Store(time.Now().UnixNano())

	sh := fc.shard(e.path)
	sh.mu.Lock()
	if old, ok := sh.items[e.path]; ok {
		fc.used.Add(-old.Value.(*fileEntry).size)
		sh.lru.Remove(old)
	}
	sh.items[e.path] = sh.lru.PushFront(e)
	sh.mu.Unlock()
	fc.used.Add(e.size)

	fc.evictIfOver()
}

func (fc *fileCache) remove(path string) {
	sh := fc.shard(path)
	sh.mu.Lock()
	if el, ok := sh.items[path]; ok {
		fc.used.Add(-el.Value.(*fileEntry).size)
		sh.lru.Remove(el)
		delete(sh.items, path)
	}
	sh.mu.Unlock()
}

// evictIfOver drops least-recently-used entries until the cache is back inside
// its budget. It walks shards rather than maintaining one global LRU: an exact
// global ordering would need a lock every request, and approximate is enough
// for a cache whose entries are all cheap to rebuild.
func (fc *fileCache) evictIfOver() {
	for i := 0; fc.used.Load() > fc.maxBytes && i < fileCacheShards*2; i++ {
		sh := &fc.shards[i%fileCacheShards]
		sh.mu.Lock()
		if el := sh.lru.Back(); el != nil {
			e := el.Value.(*fileEntry)
			sh.lru.Remove(el)
			delete(sh.items, e.path)
			fc.used.Add(-e.size)
		}
		sh.mu.Unlock()
	}
}

// Len reports how many files are cached, for tests and stats.
func (fc *fileCache) Len() int {
	if fc == nil {
		return 0
	}
	n := 0
	for i := range fc.shards {
		fc.shards[i].mu.Lock()
		n += len(fc.shards[i].items)
		fc.shards[i].mu.Unlock()
	}
	return n
}
