package cache

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const shardCount = 256

// MemoryCache is a sharded in-memory cache with LRU eviction.
type MemoryCache struct {
	shards    [shardCount]shard
	maxBytes  int64
	usedBytes atomic.Int64
	hits      atomic.Int64
	misses    atomic.Int64
	stales    atomic.Int64
}

type shard struct {
	mu    sync.RWMutex
	items map[string]*entry
	lru   *list.List
}

type entry struct {
	key     string
	resp    *CachedResponse
	size    int64
	element *list.Element
}

// NewMemoryCache creates a memory cache with the given byte limit.
func NewMemoryCache(maxBytes int64) *MemoryCache {
	mc := &MemoryCache{maxBytes: maxBytes}
	for i := range mc.shards {
		mc.shards[i].items = make(map[string]*entry)
		mc.shards[i].lru = list.New()
	}
	return mc
}

// Get looks up a key and returns the response and cache status.
func (mc *MemoryCache) Get(key string) (*CachedResponse, string) {
	s := mc.getShard(key)
	s.mu.RLock()
	e, ok := s.items[key]
	s.mu.RUnlock()

	if !ok {
		mc.misses.Add(1)
		return nil, StatusMiss
	}

	resp := e.resp

	if resp.IsFresh() {
		// Move to front (promote in LRU)
		s.mu.Lock()
		s.lru.MoveToFront(e.element)
		s.mu.Unlock()
		mc.hits.Add(1)
		return resp, StatusHit
	}

	if resp.IsStale() {
		mc.stales.Add(1)
		return resp, StatusStale
	}

	// Expired beyond grace — treat as miss
	mc.misses.Add(1)
	return nil, StatusMiss
}

// Set stores a response in the cache.
func (mc *MemoryCache) Set(key string, resp *CachedResponse) {
	size := resp.Size()
	s := mc.getShard(key)

	s.mu.Lock()
	defer s.mu.Unlock()

	// If key exists, remove old entry first
	if old, ok := s.items[key]; ok {
		mc.usedBytes.Add(-old.size)
		s.lru.Remove(old.element)
		delete(s.items, key)
	}

	// Evict LRU entries from this shard if over global memory limit
	for mc.usedBytes.Load()+size > mc.maxBytes && s.lru.Len() > 0 {
		mc.evictLRU(s)
	}

	// If still over limit (other shards), skip storing
	if mc.usedBytes.Load()+size > mc.maxBytes {
		return
	}

	e := &entry{key: key, resp: resp, size: size}
	e.element = s.lru.PushFront(e)
	s.items[key] = e
	mc.usedBytes.Add(size)
}

// Delete removes a single key.
func (mc *MemoryCache) Delete(key string) {
	s := mc.getShard(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.items[key]; ok {
		mc.usedBytes.Add(-e.size)
		s.lru.Remove(e.element)
		delete(s.items, key)
	}
}

// PurgeByTag removes all entries matching any of the given tags.
func (mc *MemoryCache) PurgeByTag(tags ...string) int {
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	count := 0
	for i := range mc.shards {
		s := &mc.shards[i]
		s.mu.Lock()
		for key, e := range s.items {
			for _, t := range e.resp.Tags {
				if tagSet[t] {
					mc.usedBytes.Add(-e.size)
					s.lru.Remove(e.element)
					delete(s.items, key)
					count++
					break
				}
			}
		}
		s.mu.Unlock()
	}
	return count
}

// PurgeAll clears the entire cache.
func (mc *MemoryCache) PurgeAll() {
	for i := range mc.shards {
		s := &mc.shards[i]
		s.mu.Lock()
		s.items = make(map[string]*entry)
		s.lru.Init()
		s.mu.Unlock()
	}
	mc.usedBytes.Store(0)
}

// Stats returns cache statistics.
func (mc *MemoryCache) Stats() (hits, misses, stales, usedBytes int64) {
	return mc.hits.Load(), mc.misses.Load(), mc.stales.Load(), mc.usedBytes.Load()
}

// Len returns total number of cached entries.
func (mc *MemoryCache) Len() int {
	total := 0
	for i := range mc.shards {
		s := &mc.shards[i]
		s.mu.RLock()
		total += len(s.items)
		s.mu.RUnlock()
	}
	return total
}

func (mc *MemoryCache) evictLRU(s *shard) {
	back := s.lru.Back()
	if back == nil {
		return
	}
	e := back.Value.(*entry)
	mc.usedBytes.Add(-e.size)
	s.lru.Remove(back)
	delete(s.items, e.key)
}

func (mc *MemoryCache) getShard(key string) *shard {
	return &mc.shards[shardIdx(key)]
}

func shardIdx(key string) uint8 {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return uint8(h)
}

// StartCleanup runs periodic eviction of expired entries.
// The goroutine exits when ctx is cancelled.
func (mc *MemoryCache) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mc.cleanExpired()
			}
		}
	}()
}

func (mc *MemoryCache) cleanExpired() {
	for i := range mc.shards {
		s := &mc.shards[i]
		s.mu.Lock()
		for key, e := range s.items {
			if e.resp.IsExpired() {
				mc.usedBytes.Add(-e.size)
				s.lru.Remove(e.element)
				delete(s.items, key)
			}
		}
		s.mu.Unlock()
	}
}
