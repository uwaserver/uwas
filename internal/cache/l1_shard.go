package cache

import (
	"sync/atomic"
)

// shardStats holds lock-free per-shard hit/miss counters.
// Using atomic.Int64 avoids torn reads when Stats() iterates across shards.
type shardStats struct {
	hits   atomic.Int64
	misses atomic.Int64
}

// addHit atomically increments the hit counter.
func (s *shardStats) addHit() { s.hits.Add(1) }

// addMiss atomically increments the miss counter.
func (s *shardStats) addMiss() { s.misses.Add(1) }

// load returns current hit/miss counts as a snapshot.
func (s *shardStats) load() (hits, misses int64) {
	return s.hits.Load(), s.misses.Load()
}
