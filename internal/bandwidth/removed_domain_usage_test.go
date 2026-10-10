package bandwidth

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// A deleted domain's usage must not pass to a re-added domain with the same
// hostname, while a configured domain keeps its counters even with bandwidth
// disabled (F775).
func TestUpdateDomainsDropsRemovedDomainUsage(t *testing.T) {
	limited := func(host string) config.Domain {
		return config.Domain{Host: host, Bandwidth: config.BandwidthConfig{Enabled: true, DailyLimit: 1000, Action: "block"}}
	}

	m := NewManager([]config.Domain{limited("a.test"), limited("c.test")})
	m.Record("a.test", 5000)
	if !m.IsBlocked("a.test") {
		t.Fatal("a.test should be blocked after exceeding its limit")
	}
	m.UpdateDomains([]config.Domain{limited("c.test")})
	m.UpdateDomains([]config.Domain{limited("a.test"), limited("c.test")})
	if st := m.GetStatus("a.test"); st == nil || st.DailyBytes != 0 || st.Blocked {
		t.Fatalf("re-added a.test inherited usage: %+v", st)
	}

	m = NewManager([]config.Domain{limited("keep.test")})
	m.Record("keep.test", 700)
	m.UpdateDomains([]config.Domain{{Host: "keep.test"}})
	m.UpdateDomains([]config.Domain{limited("keep.test")})
	if st := m.GetStatus("keep.test"); st == nil || st.DailyBytes != 700 {
		t.Fatalf("configured keep.test lost usage while bandwidth was disabled: %+v", st)
	}
}
