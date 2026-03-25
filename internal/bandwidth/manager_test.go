package bandwidth

import (
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func testDomains(bw config.BandwidthConfig) []config.Domain {
	return []config.Domain{
		{Host: "example.com", Bandwidth: bw},
	}
}

func TestRecordNoBandwidthConfig(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "example.com"},
	})

	blocked, throttled := m.Record("example.com", 1024)
	if blocked || throttled {
		t.Error("expected no block/throttle for domain without bandwidth config")
	}
}

func TestRecordBlockWhenExceeded(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		Action:       "block",
	}))

	// First request: 500 bytes — should be fine
	blocked, _ := m.Record("example.com", 500)
	if blocked {
		t.Error("should not be blocked at 500/1000")
	}

	// Second request: 600 bytes — total 1100, exceeds 1000
	blocked, _ = m.Record("example.com", 600)
	if !blocked {
		t.Error("should be blocked at 1100/1000")
	}
}

func TestRecordThrottleAt80Pct(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		Action:       "throttle",
	}))

	// 799 bytes — under 80%
	_, throttled := m.Record("example.com", 799)
	if throttled {
		t.Error("should not be throttled at 799/1000 (79.9%)")
	}

	// +10 bytes = 809 — over 80%
	_, throttled = m.Record("example.com", 10)
	if !throttled {
		t.Error("should be throttled at 809/1000 (80.9%)")
	}
}

func TestRecordDailyLimit(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 500,
		Action:     "block",
	}))

	blocked, _ := m.Record("example.com", 501)
	if !blocked {
		t.Error("should be blocked when daily limit exceeded")
	}
}

func TestAlertFunc(t *testing.T) {
	var alertCount atomic.Int32

	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "throttle",
	}))

	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		alertCount.Add(1)
	})

	// Push to 90% (alert fires at 90-91%)
	m.Record("example.com", 90)

	if alertCount.Load() != 1 {
		t.Errorf("expected 1 alert at 90%%, got %d", alertCount.Load())
	}
}

func TestGetStatus(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		DailyLimit:   500,
	}))

	m.Record("example.com", 250)

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status, got nil")
	}
	if status.MonthlyBytes != 250 {
		t.Errorf("expected MonthlyBytes=250, got %d", status.MonthlyBytes)
	}
	if status.DailyBytes != 250 {
		t.Errorf("expected DailyBytes=250, got %d", status.DailyBytes)
	}
	if status.MonthlyPct != 25.0 {
		t.Errorf("expected MonthlyPct=25.0, got %.1f", status.MonthlyPct)
	}
}

func TestGetAllStatus(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "a.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 1000}},
		{Host: "b.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 2000}},
	})

	m.Record("a.com", 100)
	m.Record("b.com", 200)

	statuses := m.GetAllStatus()
	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}
}

func TestReset(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		Action:       "block",
	}))

	m.Record("example.com", 1100)

	// Should be blocked
	status := m.GetStatus("example.com")
	if !status.Blocked {
		t.Error("expected blocked after exceeding limit")
	}

	m.Reset("example.com")

	status = m.GetStatus("example.com")
	if status.Blocked {
		t.Error("expected not blocked after reset")
	}
	if status.MonthlyBytes != 0 {
		t.Errorf("expected MonthlyBytes=0 after reset, got %d", status.MonthlyBytes)
	}
}

func TestUnknownDomain(t *testing.T) {
	m := NewManager(nil)

	blocked, throttled := m.Record("unknown.com", 1024)
	if blocked || throttled {
		t.Error("expected no block/throttle for unknown domain")
	}

	status := m.GetStatus("unknown.com")
	if status != nil {
		t.Error("expected nil status for unknown domain")
	}
}

func TestUpdateDomains(t *testing.T) {
	m := NewManager(nil)

	// Initially no domains
	statuses := m.GetAllStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses initially, got %d", len(statuses))
	}

	// Add a domain
	m.UpdateDomains([]config.Domain{
		{Host: "new.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 5000}},
	})

	m.Record("new.com", 100)
	status := m.GetStatus("new.com")
	if status == nil {
		t.Fatal("expected status after UpdateDomains")
	}
	if status.MonthlyBytes != 100 {
		t.Errorf("expected MonthlyBytes=100, got %d", status.MonthlyBytes)
	}
}
