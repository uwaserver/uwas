package analytics

import (
	"fmt"
	"strings"
	"testing"
)

// Every DomainStats embeds a ~400 KB minute ring, and RecordFull is fed the
// raw request Host. Spellings the router serves as one domain (port, case,
// trailing dot) must share one entry, or each one allocates another ring.
func TestRecordFullFoldsHostSpellings(t *testing.T) {
	c := New()
	hosts := []string{"example.com", "EXAMPLE.com", "example.com."}
	for p := 1; p <= 32; p++ {
		hosts = append(hosts, fmt.Sprintf("example.com:%d", p))
	}
	for _, h := range hosts {
		c.RecordFull(h, "/", "198.51.100.7:4000", "", "", 200, 1)
	}
	all := c.GetAll()
	if len(all) != 1 || all[0].Host != "example.com" || all[0].PageViews != int64(len(hosts)) {
		t.Fatalf("want one example.com entry with %d views, got %d entries: %+v", len(hosts), len(all), all)
	}
	if snap := c.GetHost("Example.com:443"); snap == nil || snap.PageViews != int64(len(hosts)) {
		t.Fatalf("GetHost on a spelling variant = %+v", snap)
	}

	c.RecordFull(strings.Repeat("a", 254), "/", "198.51.100.7:4000", "", "", 200, 1)
	if n := len(c.GetAll()); n != 1 {
		t.Fatalf("host longer than a DNS name was tracked: %d entries", n)
	}
}

// The Paths/Referrers entry caps only bound memory if each key is bounded too.
func TestRecordFullBoundsKeyLength(t *testing.T) {
	c := New()
	long := "/" + strings.Repeat("p", 64*1024)
	c.RecordFull("example.com", long, "198.51.100.7:4000", "http://"+strings.Repeat("r", 64*1024)+"/", "", 200, 1)

	v, _ := c.domains.Load("example.com")
	ds := v.(*DomainStats)
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for k := range ds.Paths {
		if len(k) > 2048 {
			t.Fatalf("stored path key is %d bytes, want <= 2048", len(k))
		}
	}
	if len(ds.Referrers) != 0 {
		t.Fatalf("overlong referrer host stored: %d keys", len(ds.Referrers))
	}
}
