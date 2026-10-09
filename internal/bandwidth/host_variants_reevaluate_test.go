package bandwidth

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Regression: Record keyed usage on the raw request host, but the vhost
// router also serves a domain under its aliases, the implicit www./apex
// variant, wildcard subdomains and a trailing-dot host. Traffic through any
// of those was never billed, so a limit could be bypassed simply by
// requesting www.<domain>.
func TestRecordBillsRouterHostVariants(t *testing.T) {
	bw := config.BandwidthConfig{Enabled: true, DailyLimit: 1000, Action: "block"}
	for _, tc := range []struct{ reqHost, key string }{
		{"www.example.com", "example.com"},
		{"alias.example.net", "example.com"},
		{"example.com.", "example.com"},
		{"shop.wild.test", "*.wild.test"},
	} {
		m := NewManager([]config.Domain{
			{Host: "example.com", Aliases: []string{"alias.example.net"}, Bandwidth: bw},
			{Host: "*.wild.test", Bandwidth: bw},
		})
		if blocked, _ := m.Record(tc.reqHost, 2000); !blocked || !m.IsBlocked(tc.key) {
			t.Errorf("Record(%q) blocked=%v IsBlocked(%q)=%v, want both true", tc.reqHost, blocked, tc.key, m.IsBlocked(tc.key))
		}
	}

	// Another tenant's explicit www host must not be billed to the apex domain.
	m := NewManager([]config.Domain{
		{Host: "example.com", Bandwidth: bw},
		{Host: "www.example.com"},
	})
	m.Record("www.example.com", 2000)
	if st := m.GetStatus("example.com"); st == nil || st.DailyBytes != 0 {
		t.Errorf("explicit www of another domain billed to apex: %+v", st)
	}
}

// Regression: Record only ever set Blocked=true, so raising the limit or
// switching the action away from "block" left the domain answering 503 until
// the window rolled over (up to 30 days for a monthly limit).
func TestBlockedReevaluatedOnLimitChange(t *testing.T) {
	dom := func(limit int64, action string) []config.Domain {
		return []config.Domain{{Host: "example.com", Bandwidth: config.BandwidthConfig{
			Enabled: true, MonthlyLimit: config.ByteSize(limit), Action: action}}}
	}
	for _, tc := range []struct {
		limit  int64
		action string
	}{{10000, "block"}, {1000, "throttle"}, {1000, "alert"}} {
		m := NewManager(dom(1000, "block"))
		if blocked, _ := m.Record("example.com", 1500); !blocked {
			t.Fatal("setup: expected block")
		}
		m.UpdateDomains(dom(tc.limit, tc.action))
		if m.IsBlocked("example.com") {
			t.Errorf("limit=%d action=%q: still blocked after reload", tc.limit, tc.action)
		}
		m.Record("example.com", 100)
		if m.IsBlocked("example.com") {
			t.Errorf("limit=%d action=%q: still blocked after Record", tc.limit, tc.action)
		}
	}

	m := NewManager(dom(10000, "block"))
	m.Record("example.com", 1500)
	m.UpdateDomains(dom(1000, "block"))
	if !m.IsBlocked("example.com") {
		t.Error("lowered limit below usage did not block at reload")
	}
}
