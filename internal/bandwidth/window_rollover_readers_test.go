package bandwidth

import (
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// An ended window must stop blocking and stop showing its usage even when no
// request was recorded since: the server's pre-handler IsBlocked answered the
// first request of the new day with 503 + Retry-After, and the dashboard showed
// an idle domain blocked with yesterday's usage (F67000).
func TestReadersRollOverEndedWindow(t *testing.T) {
	newMgr := func(daily, monthly int64) (*Manager, *DomainUsage) {
		m := NewManager([]config.Domain{{Host: "ex.com", Bandwidth: config.BandwidthConfig{
			Enabled: true, DailyLimit: config.ByteSize(daily), MonthlyLimit: config.ByteSize(monthly), Action: "block"}}})
		m.mu.RLock()
		u := m.usage["ex.com"]
		m.mu.RUnlock()
		return m, u
	}
	endDay := func(u *DomainUsage) {
		u.mu.Lock()
		u.DailyReset = u.DailyReset.Add(-25 * time.Hour)
		u.mu.Unlock()
	}

	m, u := newMgr(1000, 1<<40)
	m.Record("ex.com", 1000)
	if !m.IsBlocked("ex.com") {
		t.Fatal("daily limit reached: want blocked within the day")
	}
	endDay(u)
	if m.IsBlocked("ex.com") {
		t.Error("IsBlocked after the day window ended = true, want false")
	}
	if st := m.GetStatus("ex.com"); st.Blocked || st.DailyBytes != 0 || st.MonthlyBytes != 1000 {
		t.Errorf("GetStatus after the day ended: blocked=%v daily=%d monthly=%d, want false/0/1000",
			st.Blocked, st.DailyBytes, st.MonthlyBytes)
	}

	// A new day does not lift a block the month still owes.
	m, u = newMgr(1000, 1500)
	m.Record("ex.com", 1000)
	endDay(u)
	m.Record("ex.com", 600)
	endDay(u)
	if !m.IsBlocked("ex.com") {
		t.Error("monthly limit still exceeded after the day ended: want blocked")
	}
}
