package server

// F51200: per-location rate-limit counters are keyed "<host>|<location>|<client>".
// A removed domain's counters stayed in the map for up to the whole window
// (hours for a "5 per hour" login limit), so a hostname handed to the next
// tenant inherited the previous owner's exhausted budget and answered 429
// to its first visitors.

import (
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

func limiterKeys(s *Server) []string {
	var keys []string
	s.locationLimiters.Range(func(k, _ any) bool {
		keys = append(keys, k.(string))
		return true
	})
	return keys
}

func TestRemovedDomainLosesItsLocationLimiterCounters(t *testing.T) {
	s := &Server{}
	now := time.Now()
	live := func() *rateLimitEntry {
		return &rateLimitEntry{windowStart: now, count: 5, lastAccess: now, window: time.Hour}
	}
	s.locationLimiters.Store("gone.test|/login|203.0.113.7", live())
	s.locationLimiters.Store("gone.test|/api|203.0.113.8", live())
	s.locationLimiters.Store("keep.test|/login|203.0.113.7", live())
	s.locationLimiters.Store("keep.test:8080|/login|203.0.113.7", live())

	s.pruneDomainStats([]config.Domain{{Host: "keep.test"}, {Host: "keep.test:8080"}})

	keys := limiterKeys(s)
	t.Logf("EXPECTED: only keep.test counters remain\nACTUAL: %v", keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "gone.test|") {
			t.Fatalf("PROBLEM CONFIRMED: removed domain kept its counter %q", k)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("control: configured domains must keep their counters, got %v", keys)
	}
}
