package cache

import (
	"testing"
	"time"
)

// TestMemoryCachePurgeAllOrphanPromotion is a regression test for size
// accounting corruption caused by PurgeAll's former use of lru.Init().
//
// container/list.Init() resets the root WITHOUT clearing detached elements'
// list stamps. Get holds an element reference across a lock gap
// (RUnlock → readCount sample → Lock); if PurgeAll ran inside that gap, the
// resumed MoveToFront(e.element) passed its e.list != l guard (stale stamp)
// and resurrected the orphan into the fresh list without incrementing len —
// a phantom invisible to Len()/Front()/Back() while len==0. The next Set on
// the same shard made the phantom the LRU back, so evictLRU evicted IT
// instead of the live entry — double-subtracting usedBytes (PurgeAll had
// already freed that size) and deleting the live entry's key mapping via
// the phantom's stale key. Accounting drifted below reality, so maxBytes
// was silently exceeded.
//
// Production path: admin cache purge-all concurrent with read traffic (the
// 1-in-16 sampled LRU promotion). The fix removes elements individually in
// PurgeAll — lru.Remove clears each element's stamp, making a racing
// MoveToFront a harmless no-op.
func TestMemoryCachePurgeAllOrphanPromotion(t *testing.T) {
	mc := NewMemoryCache(1 << 20)
	respA := &CachedResponse{Body: []byte("A"), Created: time.Now(), TTL: time.Minute}
	sizeA := respA.Size()
	mc.Set("A", respA)
	s := mc.getShard("A")

	// Get's promotion path, first half — exact statements from Get:
	s.mu.RLock()
	e := s.items["A"]
	s.mu.RUnlock()
	if e == nil {
		t.Fatal("setup: entry A missing")
	}

	// PurgeAll interleaves inside Get's lock gap (admin "purge all").
	mc.PurgeAll()

	// Force the 1-in-16 promotion sample to hit.
	s.readCount.Store(lruPromotionRate - 1)

	// Get's promotion path, second half — exact statements from Get:
	if s.readCount.Add(1)%lruPromotionRate == 0 {
		s.mu.Lock()
		s.lru.MoveToFront(e.element)
		s.mu.Unlock()
	}

	// Re-insert on the SAME key (guaranteed same shard as any phantom).
	respB := &CachedResponse{Body: []byte("BBBB"), Created: time.Now(), TTL: time.Minute}
	sizeB := respB.Size()
	mc.Set("A", respB)

	// Set's eviction call, verbatim, under the shard lock as Set holds it.
	s.mu.Lock()
	mc.evictLRU(s)
	s.mu.Unlock()

	// Correct behavior: the eviction removed the only live entry, so the
	// accounting must be exactly zero.
	if _, _, _, used := mc.Stats(); used != 0 {
		t.Fatalf("usedBytes=%d after evicting the only live entry (sizeA=%d sizeB=%d) — evictLRU evicted a resurrected phantom, double-subtracting its size; maxBytes accounting is corrupted", used, sizeA, sizeB)
	}
	if _, ok := s.items["A"]; ok {
		t.Fatal("live entry survived eviction — a phantom was evicted instead")
	}
}

// TestMemoryCacheDeletePromotionSafe is the control: Delete removes via
// lru.Remove, which clears the element's stamp, so a racing MoveToFront is
// a no-op and accounting stays exact.
func TestMemoryCacheDeletePromotionSafe(t *testing.T) {
	mc := NewMemoryCache(1 << 20)
	mc.Set("A", &CachedResponse{Body: []byte("A"), Created: time.Now(), TTL: time.Minute})
	s := mc.getShard("A")

	s.mu.RLock()
	e := s.items["A"]
	s.mu.RUnlock()

	mc.Delete("A")

	s.readCount.Store(lruPromotionRate - 1)
	if s.readCount.Add(1)%lruPromotionRate == 0 {
		s.mu.Lock()
		s.lru.MoveToFront(e.element)
		s.mu.Unlock()
	}

	respB := &CachedResponse{Body: []byte("BB"), Created: time.Now(), TTL: time.Minute}
	sizeB := respB.Size()
	mc.Set("A", respB)

	s.mu.Lock()
	mc.evictLRU(s)
	s.mu.Unlock()

	if _, ok := s.items["A"]; ok {
		t.Fatal("control: live entry survived eviction")
	}
	if _, _, _, used := mc.Stats(); used != 0 {
		t.Fatalf("control: usedBytes=%d, want 0 (sizeB=%d)", used, sizeB)
	}
}
