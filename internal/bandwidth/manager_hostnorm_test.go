package bandwidth

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Regression: IsBlocked, GetStatus and Reset looked the host up in m.usage
// RAW, while Record and Middleware ran it through normalizeHost first.
//
// manager.go documents the contract explicitly:
//
//	"map keys, which are the configured domain hosts (normalized). The live
//	 dispatch path records usage with the raw request Host — which may carry
//	 a port (example.com:8443) or mixed case — so callers must not be relied
//	 on to pre-normalize."
//
// The admin API passes r.PathValue("host") straight through, so these three
// resolved or silently failed to resolve depending only on how the host was
// spelled. Reset was the worst: it returns nothing, so handleBandwidthReset
// replied {"status":"reset"} even when the counters were untouched.

func normTestDomain() config.Domain {
	return config.Domain{
		Host: "example.com",
		Bandwidth: config.BandwidthConfig{
			Enabled:      true,
			DailyLimit:   1000,
			MonthlyLimit: 10000,
			Action:       "block",
		},
	}
}

// rawHost is how the live dispatch path and an admin URL path value spell the
// same domain: a real request Host carrying a port, with mixed case.
const normRawHost = "Example.com:8443"

func TestGetStatusAndIsBlockedNormalizeHost(t *testing.T) {
	m := NewManager([]config.Domain{normTestDomain()})
	// Push past the daily limit so the domain is genuinely over quota.
	m.Record("example.com", 2000)

	if !m.IsBlocked("example.com") {
		t.Fatalf("control: IsBlocked(%q) = false after exceeding the daily limit", "example.com")
	}
	if st := m.GetStatus("example.com"); st == nil || st.DailyBytes != 2000 {
		t.Fatalf("control: GetStatus(%q) = %+v, want DailyBytes 2000", "example.com", st)
	}

	// Same domain, spelled the way a live request Host arrives.
	if st := m.GetStatus(normRawHost); st == nil {
		t.Fatalf("GetStatus(%q) = nil for a domain that exists, is enabled and is "+
			"over quota: the lookup is not normalized", normRawHost)
	} else if st.DailyBytes != 2000 {
		t.Errorf("GetStatus(%q).DailyBytes = %d, want 2000", normRawHost, st.DailyBytes)
	}
	if !m.IsBlocked(normRawHost) {
		t.Errorf("IsBlocked(%q) = false for a domain that is over its daily limit", normRawHost)
	}
}

func TestResetNormalizesHost(t *testing.T) {
	m := NewManager([]config.Domain{normTestDomain()})
	m.Record("example.com", 2000)

	m.Reset(normRawHost)

	if got := m.GetStatus("example.com").DailyBytes; got != 0 {
		t.Fatalf("Reset(%q) was a silent no-op — DailyBytes is still %d; the "+
			"operator is told the quota was cleared while the counter is untouched",
			normRawHost, got)
	}
}

// An unknown host must still be unknown after normalization — normalizing
// must not turn a miss into a hit.
func TestUnknownHostStillMissesAfterNormalization(t *testing.T) {
	m := NewManager([]config.Domain{normTestDomain()})

	if st := m.GetStatus("absent.com:8443"); st != nil {
		t.Errorf("GetStatus(%q) = %+v, want nil for a domain that does not exist", "absent.com:8443", st)
	}
	if m.IsBlocked("absent.com:8443") {
		t.Errorf("IsBlocked(%q) = true for a domain that does not exist", "absent.com:8443")
	}
	m.Reset("absent.com:8443") // must be a safe no-op
	if st := m.GetStatus("example.com"); st == nil {
		t.Errorf("control: normalizing a miss disturbed an existing domain")
	}
}
