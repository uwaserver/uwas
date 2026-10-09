package bandwidth

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Regression (F316): "example.com" and "example.com:8080" are two tenants
// (the router gives a port-qualified host only its port), but bandwidth keyed
// both on the port-stripped host. One tenant's traffic was billed to the
// other, and crossing that shared counter blocked the wrong site with 503.
func TestPortQualifiedTenantKeyedSeparately(t *testing.T) {
	lim := config.BandwidthConfig{Enabled: true, DailyLimit: 1000, MonthlyLimit: 100000, Action: "block"}
	for _, portFirst := range []bool{false, true} {
		doms := []config.Domain{{Host: "example.com", Bandwidth: lim}, {Host: "example.com:8080", Bandwidth: lim}}
		if portFirst {
			doms[0], doms[1] = doms[1], doms[0]
		}
		m := NewManager(doms)

		if blocked, _ := m.Record("example.com:8080", 5000); !blocked {
			t.Fatalf("portFirst=%v: tenant :8080 over its own limit was not blocked", portFirst)
		}
		m.Record("www.example.com:443", 10)

		if got := m.GetStatus("example.com").DailyBytes; got != 10 {
			t.Errorf("portFirst=%v: example.com billed %d bytes, want 10 (its own www traffic only)", portFirst, got)
		}
		if m.IsBlocked("example.com") {
			t.Errorf("portFirst=%v: example.com blocked by the :8080 tenant's usage", portFirst)
		}
		if got := m.GetStatus("example.com:8080").DailyBytes; got != 5000 {
			t.Errorf("portFirst=%v: example.com:8080 billed %d bytes, want 5000", portFirst, got)
		}
	}
}
