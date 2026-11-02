package bandwidth

import (
	"github.com/uwaserver/uwas/internal/config"
	"testing"
)

func TestInternationalizedTenantBandwidthQuota(t *testing.T) {
	for _, configured := range []string{"bücher.example", "xn--bcher-kva.example"} {
		for _, request := range []string{"bücher.example", "xn--bcher-kva.example", "XN--BCHER-KVA.EXAMPLE.:443"} {
			m := NewManager([]config.Domain{{Host: configured, Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 10, Action: "block"}}})
			if b, _ := m.Record(request, 9); b {
				t.Fatal("blocked before limit")
			}
			if b, _ := m.Record(request, 1); !b {
				t.Fatal("quota missed", configured, request)
			}
			if s := m.GetStatus(configured); s == nil || s.MonthlyBytes != 10 || !s.Blocked {
				t.Fatal(s)
			}
			m.Reset(configured)
			if m.IsBlocked(configured) {
				t.Fatal("reset failed")
			}
		}
	}
	m := NewManager([]config.Domain{{Host: "bücher.example:8080", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 10, Action: "block"}}, {Host: "bücher.example", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 100, Action: "block"}}})
	if b, _ := m.Record("xn--bcher-kva.example:8080", 10); !b {
		t.Fatal("port quota")
	}
	if b, _ := m.Record("xn--bcher-kva.example:443", 10); b {
		t.Fatal("cross-port billing")
	}
	wild := NewManager([]config.Domain{{Host: "*.bücher.example", Bandwidth: config.BandwidthConfig{Enabled: true, DailyLimit: 10, Action: "block"}}})
	if b, _ := wild.Record("api.xn--bcher-kva.example", 10); !b {
		t.Fatal("wildcard quota")
	}
}
