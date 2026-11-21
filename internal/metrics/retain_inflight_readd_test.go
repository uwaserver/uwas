package metrics

import "testing"

// A request still in flight when its domain is deleted must not leave
// counters behind for whoever re-adds the hostname: when the next domain
// change was that re-add, RetainDomains kept the entry the old owner's
// request re-created (F67200).
func TestRetainDomainsDropsInflightRecordOfRemovedHost(t *testing.T) {
	c := New()
	c.RetainDomains([]string{"x.com", "other.com"})
	c.RecordDomain("x.com", 200, 10)
	c.RetainDomains([]string{"other.com"}) // x.com deleted
	c.RecordDomain("x.com", 500, 777)      // its in-flight request completes
	c.RetainDomains([]string{"other.com", "x.com"})
	if s, ok := c.DomainStatsSnapshot()["x.com"]; ok {
		t.Fatalf("re-added x.com inherits the previous owner's counters: %v", s)
	}

	c.RecordDomain("x.com", 200, 5)
	s := c.DomainStatsSnapshot()["x.com"]
	if s == nil || s["requests"] != 1 || s["bytes_out"] != 5 {
		t.Fatalf("new tenant's request not recorded after re-add: %v", s)
	}
}
