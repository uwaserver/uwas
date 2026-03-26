package analytics

import (
	"testing"
)

// TestRecordFullNilMapsWithReferrerAndUA covers the nil-guard branches in
// RecordFull when Referrers and UserAgents maps are nil but non-empty
// referrer/UA strings are passed.
func TestRecordFullNilMapsWithReferrerAndUA(t *testing.T) {
	c := New()

	// Store a DomainStats with nil Referrers and UserAgents maps.
	stats := &DomainStats{}
	c.domains.Store("nilmaps-full.com", stats)

	// Record with non-empty referrer and UA to hit the nil-guard branches.
	c.RecordFull("nilmaps-full.com", "/page", "1.2.3.4:5678",
		"https://google.com/search", "Mozilla/5.0 Chrome/120", 200, 100)

	snap := c.GetHost("nilmaps-full.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.TopReferrers["google.com"] != 1 {
		t.Errorf("google.com referrals = %d, want 1", snap.TopReferrers["google.com"])
	}
	if snap.UserAgents["Chrome"] != 1 {
		t.Errorf("Chrome UA = %d, want 1", snap.UserAgents["Chrome"])
	}
}
