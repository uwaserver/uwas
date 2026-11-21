package cache

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// TestMemoryFullCacheLargeEntryEvictsNothing: with the cache full, an entry
// larger than what its shard holds emptied that shard and was then not stored
// (the rest of the limit is held by other shards), so every large response
// flushed 1/256 of the cache for nothing (F67901).
func TestMemoryFullCacheLargeEntryEvictsNothing(t *testing.T) {
	resp := func(n int) *CachedResponse {
		return &CachedResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"text/plain"}},
			Body: make([]byte, n), Created: time.Now(), TTL: time.Hour}
	}
	mc := NewMemoryCache(4 << 20)
	for i := 0; i < 2000; i++ {
		mc.Set("fill-"+strconv.Itoa(i), resp(4<<10))
	}
	before := mc.Len()
	for i := 0; i < 50; i++ {
		mc.Set("big-"+strconv.Itoa(i), resp(256<<10)) // over a shard's share, under the limit
	}
	if after := mc.Len(); after != before {
		t.Fatalf("large entries that could not be stored evicted %d of %d entries", before-after, before)
	}

	// A smaller entry that fits after evicting part of its shard is still stored.
	mc.Set("mid", resp(8<<10))
	if got, _ := mc.Get("mid"); got == nil {
		t.Fatal("an entry that fits after a partial shard eviction was not stored")
	}
}
