package analytics

import "testing"

// A request still in flight when its domain is deleted must not leave stats
// behind for whoever re-adds the hostname: when the next domain change was
// that re-add, RetainHosts kept the entry the old owner's request re-created
// (F67001).
func TestRetainHostsDropsInflightRecordOfRemovedHost(t *testing.T) {
	c := New()
	rec := func(path string) {
		c.RecordFull("x.com", path, "1.2.3.4:5", "https://ref.old/", "curl/8", 200, 10)
	}
	c.RetainHosts([]string{"x.com", "other.com"})
	rec("/a")
	c.RetainHosts([]string{"other.com"}) // x.com deleted
	rec("/old-owner-secret")             // its in-flight request completes
	c.RetainHosts([]string{"other.com", "x.com"})
	if s := c.GetHost("x.com"); s != nil {
		t.Fatalf("re-added x.com shows the previous owner's request: paths=%v referrers=%v", s.TopPaths, s.TopReferrers)
	}

	rec("/new")
	s := c.GetHost("x.com")
	if s == nil || s.PageViews != 1 || s.TopPaths["/new"] != 1 {
		t.Fatalf("new tenant's request not recorded after re-add: %+v", s)
	}
}
