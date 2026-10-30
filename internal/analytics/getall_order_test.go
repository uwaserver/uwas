package analytics

import (
	"fmt"
	"sort"
	"testing"
)

// F49304: GetAll returned the per-domain snapshots in the order the underlying
// map happens to iterate, which is neither sorted nor tied to anything an
// operator can see: the Analytics page polls it every 5 s and a domain's first
// request could move it anywhere in the table. The list is now ordered by host.

func TestGetAllIsSortedByHost(t *testing.T) {
	c := New()
	for i := 0; i < 12; i++ {
		host := fmt.Sprintf("site-%02d.example.com", (i*7)%12)
		c.RecordFull(host, "/", "203.0.113.9:1234", "", "curl/8", 200, 10)
	}

	hosts := func() []string {
		var out []string
		for _, s := range c.GetAll() {
			out = append(out, s.Host)
		}
		return out
	}
	got := hosts()
	t.Logf("EXPECTED: hosts in ascending order\nACTUAL: %v", got)
	if len(got) != 12 {
		t.Fatalf("got %d snapshots, want 12", len(got))
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("PROBLEM CONFIRMED: GetAll is not sorted by host: %v", got)
	}
	t.Log("PROBLEM NOT REPRODUCED")

	// A new domain slots in by name instead of landing wherever its hash does.
	c.RecordFull("aaa-first.example.com", "/", "203.0.113.9:1234", "", "curl/8", 200, 10)
	if got := hosts(); !sort.StringsAreSorted(got) || got[0] != "aaa-first.example.com" {
		t.Fatalf("a new domain did not sort into place: %v", got)
	}
}

// Control: an empty collector still returns no snapshots, and each snapshot
// keeps its counters.
func TestGetAllKeepsCountersAndEmptyCase(t *testing.T) {
	c := New()
	if n := len(c.GetAll()); n != 0 {
		t.Fatalf("empty collector returned %d snapshots", n)
	}
	c.RecordFull("a.test", "/x", "203.0.113.9:1234", "", "curl/8", 200, 100)
	c.RecordFull("a.test", "/x", "203.0.113.10:1234", "", "curl/8", 404, 50)
	all := c.GetAll()
	if len(all) != 1 || all[0].PageViews != 2 || all[0].BytesSent != 150 || all[0].UniqueIPs != 2 {
		t.Fatalf("snapshot counters changed: %+v", all)
	}
}
