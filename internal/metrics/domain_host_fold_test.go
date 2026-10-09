package metrics

import (
	"strings"
	"testing"
)

// RecordDomain is fed the raw request Host. Spellings the router serves as one
// domain must land in one entry, and a host longer than a DNS name is dropped.
func TestRecordDomainFoldsHostSpellings(t *testing.T) {
	c := New()
	for _, h := range []string{"example.com", "EXAMPLE.com", "example.com.", "example.com:443", "example.com:8080"} {
		c.RecordDomain(h, 200, 10)
	}
	c.RecordDomain(strings.Repeat("a", 254), 200, 10)

	snap := c.DomainStatsSnapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 entry, got %d: %v", len(snap), snap)
	}
	if got := snap["example.com"]["requests"]; got != 5 {
		t.Fatalf("example.com requests = %d, want 5", got)
	}
}
