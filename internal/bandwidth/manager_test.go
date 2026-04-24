package bandwidth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// newAtomicInt64 creates an atomic.Int64 with the given value.
func newAtomicInt64(v int64) *atomic.Int64 {
	var a atomic.Int64
	a.Store(v)
	return &a
}

func testDomains(bw config.BandwidthConfig) []config.Domain {
	return []config.Domain{
		{Host: "example.com", Bandwidth: bw},
	}
}

// ---------------------------------------------------------------------------
// NewManager
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	m := NewManager(nil)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.limits == nil {
		t.Error("limits map not initialized")
	}
	if m.usage == nil {
		t.Error("usage map not initialized")
	}
}

func TestNewManagerWithDomains(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "a.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 1000}},
		{Host: "b.com", Bandwidth: config.BandwidthConfig{Enabled: false, MonthlyLimit: 2000}},
	})
	if _, ok := m.limits["a.com"]; !ok {
		t.Error("expected a.com in limits")
	}
	if _, ok := m.limits["b.com"]; ok {
		t.Error("b.com should not be in limits (disabled)")
	}
	if _, ok := m.usage["a.com"]; !ok {
		t.Error("expected a.com in usage")
	}
}

// ---------------------------------------------------------------------------
// UpdateDomains
// ---------------------------------------------------------------------------

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

func TestUpdateDomainsRemovesOldAndAddsNew(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "old.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 1000}},
	})

	m.Record("old.com", 100)

	// Replace with different domain
	m.UpdateDomains([]config.Domain{
		{Host: "new.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 2000}},
	})

	if _, ok := m.limits["old.com"]; ok {
		t.Error("old.com should be removed from limits")
	}
	if _, ok := m.limits["new.com"]; !ok {
		t.Error("new.com should be in limits")
	}
}

func TestUpdateDomainsPreservesExistingUsage(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "keep.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 1000}},
	})

	m.Record("keep.com", 500)

	// Update with same domain — usage should be preserved
	m.UpdateDomains([]config.Domain{
		{Host: "keep.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 2000}},
	})

	status := m.GetStatus("keep.com")
	if status == nil {
		t.Fatal("expected status")
	}
	if status.MonthlyBytes != 500 {
		t.Errorf("expected usage preserved at 500, got %d", status.MonthlyBytes)
	}
	if status.MonthlyLimit != 2000 {
		t.Errorf("expected updated limit 2000, got %d", status.MonthlyLimit)
	}
}

func TestUpdateDomainsDisabledDomainSkipped(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "skip.com", Bandwidth: config.BandwidthConfig{Enabled: false, MonthlyLimit: 1000}},
	})

	if _, ok := m.limits["skip.com"]; ok {
		t.Error("disabled domain should not be in limits")
	}
	if _, ok := m.usage["skip.com"]; ok {
		t.Error("disabled domain should not have usage tracking")
	}
}

// ---------------------------------------------------------------------------
// Record — no config / unknown domain
// ---------------------------------------------------------------------------

func TestRecordNoBandwidthConfig(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "example.com"},
	})

	blocked, throttled := m.Record("example.com", 1024)
	if blocked || throttled {
		t.Error("expected no block/throttle for domain without bandwidth config")
	}
}

func TestRecordUnknownDomain(t *testing.T) {
	m := NewManager(nil)

	blocked, throttled := m.Record("unknown.com", 1024)
	if blocked || throttled {
		t.Error("expected no block/throttle for unknown domain")
	}
}

func TestRecordHasLimitButNoUsage(t *testing.T) {
	// Edge case: domain exists in limits but not in usage map
	m := NewManager(nil)
	m.mu.Lock()
	m.limits["orphan.com"] = config.BandwidthConfig{Enabled: true, MonthlyLimit: 100}
	// Intentionally do NOT add usage entry
	m.mu.Unlock()

	blocked, throttled := m.Record("orphan.com", 50)
	if blocked || throttled {
		t.Error("expected no block/throttle when usage entry is missing")
	}
}

// ---------------------------------------------------------------------------
// Record — block action
// ---------------------------------------------------------------------------

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

func TestRecordBlockOnDailyExceeded(t *testing.T) {
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

func TestRecordBlockMonthlyNotTriggeredWhenZero(t *testing.T) {
	// Monthly limit is 0 (disabled), daily limit set
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
		Action:     "block",
	}))

	// Under daily limit — should NOT be blocked
	blocked, _ := m.Record("example.com", 50)
	if blocked {
		t.Error("should not be blocked at 50/100 daily, monthly disabled")
	}

	// Over daily limit — should be blocked
	blocked, _ = m.Record("example.com", 60)
	if !blocked {
		t.Error("should be blocked at 110/100 daily")
	}
}

func TestRecordBlockDailyNotTriggeredWhenZero(t *testing.T) {
	// Daily limit is 0 (disabled), only monthly set
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	blocked, _ := m.Record("example.com", 50)
	if blocked {
		t.Error("should not be blocked under monthly limit, daily disabled")
	}

	blocked, _ = m.Record("example.com", 60)
	if !blocked {
		t.Error("should be blocked at 110/100 monthly")
	}
}

// ---------------------------------------------------------------------------
// Record — throttle action
// ---------------------------------------------------------------------------

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

func TestRecordThrottleDailyAt80Pct(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
		Action:     "throttle",
	}))

	// Under 80% of daily limit
	_, throttled := m.Record("example.com", 79)
	if throttled {
		t.Error("should not be throttled at 79/100 daily (79%%)")
	}

	// Over 80% of daily limit
	_, throttled = m.Record("example.com", 2)
	if !throttled {
		t.Error("should be throttled at 81/100 daily (81%%)")
	}
}

func TestRecordThrottleMonthlyBeforeDaily(t *testing.T) {
	// Monthly is hit first at 80% — daily won't even be checked
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		DailyLimit:   1000,
		Action:       "throttle",
	}))

	_, throttled := m.Record("example.com", 81)
	if !throttled {
		t.Error("should be throttled at 81%% of monthly")
	}
}

func TestRecordEmptyActionTreatedAsThrottle(t *testing.T) {
	// Empty action defaults to throttle behavior
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "",
	}))

	_, throttled := m.Record("example.com", 81)
	if !throttled {
		t.Error("empty action should be treated as throttle")
	}
}

func TestRecordNoBlockOrThrottleUnderLimits(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 10000,
		DailyLimit:   5000,
		Action:       "throttle",
	}))

	blocked, throttled := m.Record("example.com", 100)
	if blocked || throttled {
		t.Error("should not be blocked or throttled well under limits")
	}
}

// ---------------------------------------------------------------------------
// Record — alert action (neither block nor throttle)
// ---------------------------------------------------------------------------

func TestRecordAlertActionNoBlockNoThrottle(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		DailyLimit:   50,
		Action:       "alert",
	}))

	// Exceed both limits
	blocked, throttled := m.Record("example.com", 200)
	if blocked {
		t.Error("alert action should never block")
	}
	if throttled {
		t.Error("alert action should never throttle")
	}
}

// ---------------------------------------------------------------------------
// Record — counter resets
// ---------------------------------------------------------------------------

func TestRecordMonthlyReset(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		Action:       "block",
	}))

	// Record enough to block
	m.Record("example.com", 1500)

	// Manually set LastReset to >30 days ago to trigger monthly reset
	m.mu.RLock()
	usage := m.usage["example.com"]
	m.mu.RUnlock()

	usage.mu.Lock()
	usage.LastReset = time.Now().Add(-31 * 24 * time.Hour)
	usage.mu.Unlock()

	// Next record should reset counters and not block
	blocked, _ := m.Record("example.com", 100)
	if blocked {
		t.Error("should not be blocked after monthly reset")
	}

	status := m.GetStatus("example.com")
	if status.MonthlyBytes != 100 {
		t.Errorf("expected MonthlyBytes=100 after reset, got %d", status.MonthlyBytes)
	}
	if status.Blocked {
		t.Error("expected Blocked=false after reset")
	}
}

func TestRecordDailyReset(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 500,
		Action:     "block",
	}))

	// Record enough to block on daily
	m.Record("example.com", 600)

	// Manually set DailyReset to >24h ago
	m.mu.RLock()
	usage := m.usage["example.com"]
	m.mu.RUnlock()

	usage.mu.Lock()
	usage.DailyReset = time.Now().Add(-25 * time.Hour)
	usage.mu.Unlock()

	// Next record should reset daily counters
	blocked, _ := m.Record("example.com", 100)
	if blocked {
		t.Error("should not be blocked after daily reset")
	}

	status := m.GetStatus("example.com")
	if status.DailyBytes != 100 {
		t.Errorf("expected DailyBytes=100 after reset, got %d", status.DailyBytes)
	}
}

func TestRecordBothResetsSimultaneously(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		DailyLimit:   500,
		Action:       "block",
	}))

	m.Record("example.com", 1500)

	m.mu.RLock()
	usage := m.usage["example.com"]
	m.mu.RUnlock()

	usage.mu.Lock()
	usage.LastReset = time.Now().Add(-31 * 24 * time.Hour)
	usage.DailyReset = time.Now().Add(-25 * time.Hour)
	usage.mu.Unlock()

	blocked, _ := m.Record("example.com", 50)
	if blocked {
		t.Error("should not be blocked after both resets")
	}

	status := m.GetStatus("example.com")
	if status.MonthlyBytes != 50 {
		t.Errorf("expected MonthlyBytes=50, got %d", status.MonthlyBytes)
	}
	if status.DailyBytes != 50 {
		t.Errorf("expected DailyBytes=50, got %d", status.DailyBytes)
	}
}

func TestRecordResetClearsBlockedAndThrottled(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	// Exceed and get blocked
	blocked, _ := m.Record("example.com", 200)
	if !blocked {
		t.Fatal("should be blocked")
	}

	// Force monthly reset
	m.mu.RLock()
	usage := m.usage["example.com"]
	m.mu.RUnlock()

	usage.mu.Lock()
	usage.LastReset = time.Now().Add(-31 * 24 * time.Hour)
	usage.Throttled = true // also set throttled to ensure it gets cleared
	usage.mu.Unlock()

	m.Record("example.com", 10)

	status := m.GetStatus("example.com")
	if status.Blocked {
		t.Error("Blocked should be cleared after monthly reset")
	}
	if status.Throttled {
		t.Error("Throttled should be cleared after monthly reset")
	}
}

// ---------------------------------------------------------------------------
// Record — alert callbacks
// ---------------------------------------------------------------------------

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

func TestAlertMonthlyExceeded(t *testing.T) {
	var alerts []string

	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "alert",
	}))

	var mu sync.Mutex
	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		mu.Lock()
		alerts = append(alerts, limitType)
		mu.Unlock()
	})

	// Push to exactly 100% (alert fires at 100-101%)
	m.Record("example.com", 100)

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, a := range alerts {
		if a == "monthly_exceeded" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected monthly_exceeded alert, got %v", alerts)
	}
}

func TestAlertDaily90(t *testing.T) {
	var alerts []string

	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
		Action:     "alert",
	}))

	var mu sync.Mutex
	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		mu.Lock()
		alerts = append(alerts, limitType)
		mu.Unlock()
	})

	// Push to 90% daily
	m.Record("example.com", 90)

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, a := range alerts {
		if a == "daily_90" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected daily_90 alert, got %v", alerts)
	}
}

func TestAlertDailyExceeded(t *testing.T) {
	var alerts []string

	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
		Action:     "alert",
	}))

	var mu sync.Mutex
	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		mu.Lock()
		alerts = append(alerts, limitType)
		mu.Unlock()
	})

	// Push to 100% daily
	m.Record("example.com", 100)

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, a := range alerts {
		if a == "daily_exceeded" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected daily_exceeded alert, got %v", alerts)
	}
}

func TestAlertNotFiredOutsideWindow(t *testing.T) {
	var alertCount atomic.Int32

	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "alert",
	}))

	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		alertCount.Add(1)
	})

	// Push to 85% — no alert window
	m.Record("example.com", 85)
	if alertCount.Load() != 0 {
		t.Errorf("expected 0 alerts at 85%%, got %d", alertCount.Load())
	}

	// Push to 92% (past the 90-91% window) — no alert
	m.Record("example.com", 7)
	if alertCount.Load() != 0 {
		t.Errorf("expected 0 alerts at 92%% (past window), got %d", alertCount.Load())
	}
}

func TestAlertNoCallbackSet(t *testing.T) {
	// Ensure no panic when alertFn is nil
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	// No SetAlertFunc called — alertFn is nil
	blocked, _ := m.Record("example.com", 95) // 95% — would trigger alert if fn existed
	if blocked {
		t.Error("should not be blocked at 95/100 with block action")
	}
}

func TestAlertBothMonthlyAndDailyInSameRecord(t *testing.T) {
	var alerts []string

	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		DailyLimit:   100,
		Action:       "alert",
	}))

	var mu sync.Mutex
	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		mu.Lock()
		alerts = append(alerts, limitType)
		mu.Unlock()
	})

	// Both monthly and daily hit 90% simultaneously
	m.Record("example.com", 90)

	mu.Lock()
	defer mu.Unlock()
	if len(alerts) != 2 {
		t.Errorf("expected 2 alerts (monthly_90 + daily_90), got %d: %v", len(alerts), alerts)
	}
}

// ---------------------------------------------------------------------------
// SetAlertFunc
// ---------------------------------------------------------------------------

func TestSetAlertFunc(t *testing.T) {
	m := NewManager(nil)
	if m.alertFn != nil {
		t.Error("alertFn should be nil initially")
	}

	m.SetAlertFunc(func(host, limitType string, current, limit int64) {
		// no-op
	})

	if m.alertFn == nil {
		t.Error("alertFn should not be nil after SetAlertFunc")
	}
}

// ---------------------------------------------------------------------------
// GetStatus
// ---------------------------------------------------------------------------

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
	if status.DailyPct != 50.0 {
		t.Errorf("expected DailyPct=50.0, got %.1f", status.DailyPct)
	}
	if status.Host != "example.com" {
		t.Errorf("expected Host=example.com, got %s", status.Host)
	}
	if status.MonthlyLimit != 1000 {
		t.Errorf("expected MonthlyLimit=1000, got %d", status.MonthlyLimit)
	}
	if status.DailyLimit != 500 {
		t.Errorf("expected DailyLimit=500, got %d", status.DailyLimit)
	}
}

func TestGetStatusUnknownDomain(t *testing.T) {
	m := NewManager(nil)
	status := m.GetStatus("unknown.com")
	if status != nil {
		t.Error("expected nil status for unknown domain")
	}
}

func TestGetStatusNoUsageEntry(t *testing.T) {
	// Domain in limits but not in usage — should return nil
	m := NewManager(nil)
	m.mu.Lock()
	m.limits["orphan.com"] = config.BandwidthConfig{Enabled: true}
	m.mu.Unlock()

	status := m.GetStatus("orphan.com")
	if status != nil {
		t.Error("expected nil status when usage entry missing")
	}
}

func TestGetStatusZeroMonthlyLimit(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
	}))

	m.Record("example.com", 50)

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}
	if status.MonthlyPct != 0 {
		t.Errorf("expected MonthlyPct=0 when no monthly limit, got %.1f", status.MonthlyPct)
	}
	if status.DailyPct != 50.0 {
		t.Errorf("expected DailyPct=50.0, got %.1f", status.DailyPct)
	}
}

func TestGetStatusZeroDailyLimit(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
	}))

	m.Record("example.com", 50)

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}
	if status.DailyPct != 0 {
		t.Errorf("expected DailyPct=0 when no daily limit, got %.1f", status.DailyPct)
	}
	if status.MonthlyPct != 50.0 {
		t.Errorf("expected MonthlyPct=50.0, got %.1f", status.MonthlyPct)
	}
}

func TestGetStatusReflectsBlockedState(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	m.Record("example.com", 200)

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}
	if !status.Blocked {
		t.Error("expected Blocked=true")
	}
}

func TestGetStatusReflectsThrottledState(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "throttle",
	}))

	m.Record("example.com", 85)

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}
	if !status.Throttled {
		t.Error("expected Throttled=true")
	}
}

// ---------------------------------------------------------------------------
// GetAllStatus
// ---------------------------------------------------------------------------

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

	// Check all statuses are populated
	hosts := make(map[string]bool)
	for _, s := range statuses {
		hosts[s.Host] = true
	}
	if !hosts["a.com"] {
		t.Error("missing a.com in statuses")
	}
	if !hosts["b.com"] {
		t.Error("missing b.com in statuses")
	}
}

func TestGetAllStatusEmpty(t *testing.T) {
	m := NewManager(nil)
	statuses := m.GetAllStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

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

func TestResetUnknownDomain(t *testing.T) {
	m := NewManager(nil)
	// Should not panic
	m.Reset("nonexistent.com")
}

func TestResetClearsAllFields(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		DailyLimit:   50,
		Action:       "block",
	}))

	m.Record("example.com", 200)

	m.Reset("example.com")

	status := m.GetStatus("example.com")
	if status.MonthlyBytes != 0 {
		t.Errorf("expected MonthlyBytes=0, got %d", status.MonthlyBytes)
	}
	if status.DailyBytes != 0 {
		t.Errorf("expected DailyBytes=0, got %d", status.DailyBytes)
	}
	if status.Blocked {
		t.Error("expected Blocked=false")
	}
	if status.Throttled {
		t.Error("expected Throttled=false")
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func TestMiddlewareNoLimit(t *testing.T) {
	m := NewManager(nil)
	mw := m.Middleware()

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "http://nolimit.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestMiddlewareBlockedDomain(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	// Exceed limit to block
	m.Record("example.com", 200)

	mw := m.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called when domain is blocked")
	}))

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if rec.Body.String() != `{"error":"bandwidth limit exceeded"}` {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}
}

func TestMiddlewareNotBlockedPassesThrough(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 10000,
		Action:       "block",
	}))

	mw := m.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rec.Body.String())
	}
}

func TestMiddlewareUsageNilSafe(t *testing.T) {
	// Has limit but force usage to nil to test nil-safe path
	m := NewManager(nil)
	m.mu.Lock()
	m.limits["edge.com"] = config.BandwidthConfig{Enabled: true, MonthlyLimit: 100}
	// Don't add usage entry
	m.mu.Unlock()

	mw := m.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pass"))
	}))

	req := httptest.NewRequest("GET", "http://edge.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// responseWriter
// ---------------------------------------------------------------------------

func TestResponseWriterWriteSetsHeader(t *testing.T) {
	m := NewManager(nil)
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "test.com", manager: m}

	n, err := rw.Write([]byte("data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes written, got %d", n)
	}
	if rw.bytesWritten != 4 {
		t.Errorf("expected bytesWritten=4, got %d", rw.bytesWritten)
	}
	if !rw.wroteHeader {
		t.Error("expected wroteHeader=true after Write")
	}
	// The implicit status code should be 200
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestResponseWriterWriteAfterExplicitHeader(t *testing.T) {
	m := NewManager(nil)
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "test.com", manager: m}

	rw.WriteHeader(http.StatusCreated)
	n, err := rw.Write([]byte("created"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 7 {
		t.Errorf("expected 7 bytes written, got %d", n)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
}

func TestResponseWriterMultipleWrites(t *testing.T) {
	m := NewManager(nil)
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "test.com", manager: m}

	rw.Write([]byte("abc"))
	rw.Write([]byte("def"))

	if rw.bytesWritten != 6 {
		t.Errorf("expected bytesWritten=6, got %d", rw.bytesWritten)
	}
}

func TestResponseWriterDuplicateWriteHeader(t *testing.T) {
	m := NewManager(nil)
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "test.com", manager: m}

	rw.WriteHeader(http.StatusNotFound)
	rw.WriteHeader(http.StatusOK) // should be ignored

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 (first call), got %d", rec.Code)
	}
}

func TestResponseWriterFlushRecordsBandwidth(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 10000,
	}))

	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "example.com", manager: m}

	rw.Write([]byte("some data here"))
	rw.Flush()

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}
	if status.MonthlyBytes != 14 {
		t.Errorf("expected MonthlyBytes=14 after flush, got %d", status.MonthlyBytes)
	}
}

func TestResponseWriterFlushNoBytesNoRecord(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 10000,
	}))

	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "example.com", manager: m}

	// Flush without writing — should not record
	rw.Flush()

	status := m.GetStatus("example.com")
	if status != nil && status.MonthlyBytes != 0 {
		t.Error("Flush should not record when bytesWritten=0")
	}
}

// ---------------------------------------------------------------------------
// Handler — API endpoints
// ---------------------------------------------------------------------------

func TestHandlerAllStatus(t *testing.T) {
	m := NewManager([]config.Domain{
		{Host: "a.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 1000}},
	})

	m.Record("a.com", 100)

	allHandler, _ := m.Handler()

	req := httptest.NewRequest("GET", "/api/v1/bandwidth", nil)
	rec := httptest.NewRecorder()
	allHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var statuses []Status
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(statuses) != 1 {
		t.Errorf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Host != "a.com" {
		t.Errorf("expected host a.com, got %s", statuses[0].Host)
	}
}

func TestHandlerHostStatusFound(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		DailyLimit:   500,
	}))

	m.Record("example.com", 200)

	_, hostHandler := m.Handler()

	// Use Go 1.22+ ServeMux pattern for PathValue
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/bandwidth/{host}", hostHandler)

	req := httptest.NewRequest("GET", "/api/v1/bandwidth/example.com", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var status Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if status.Host != "example.com" {
		t.Errorf("expected host example.com, got %s", status.Host)
	}
	if status.MonthlyBytes != 200 {
		t.Errorf("expected MonthlyBytes=200, got %d", status.MonthlyBytes)
	}
}

func TestHandlerHostStatusNotFound(t *testing.T) {
	m := NewManager(nil)

	_, hostHandler := m.Handler()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/bandwidth/{host}", hostHandler)

	req := httptest.NewRequest("GET", "/api/v1/bandwidth/unknown.com", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	body := rec.Body.String()
	expectedBody := `{"error":"domain not found or bandwidth not enabled"}`
	if body != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, body)
	}
}

// ---------------------------------------------------------------------------
// BlockResponse
// ---------------------------------------------------------------------------

func TestBlockResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	BlockResponse(rec)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	expected := `{"error":"bandwidth limit exceeded","code":"BANDWIDTH_EXCEEDED"}`
	if rec.Body.String() != expected {
		t.Errorf("expected body %q, got %q", expected, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// ThrottleDelay
// ---------------------------------------------------------------------------

func TestThrottleDelay(t *testing.T) {
	d := ThrottleDelay()
	if d != 500*time.Millisecond {
		t.Errorf("expected 500ms, got %v", d)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentRecordAndStatus(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100000,
		DailyLimit:   50000,
		Action:       "throttle",
	}))

	var wg sync.WaitGroup
	const goroutines = 50
	const recordsPerGoroutine = 100

	// Concurrent records
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				m.Record("example.com", 1)
			}
		}()
	}

	// Concurrent status reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = m.GetStatus("example.com")
				_ = m.GetAllStatus()
			}
		}()
	}

	wg.Wait()

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}

	expectedBytes := int64(goroutines * recordsPerGoroutine)
	if status.MonthlyBytes != expectedBytes {
		t.Errorf("expected MonthlyBytes=%d, got %d", expectedBytes, status.MonthlyBytes)
	}
}

func TestConcurrentRecordAndReset(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100000,
		Action:       "block",
	}))

	var wg sync.WaitGroup

	// Record in parallel
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Record("example.com", 1)
			}
		}()
	}

	// Reset in parallel
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				m.Reset("example.com")
			}
		}()
	}

	wg.Wait()
	// Just ensure no panics/races — final value is non-deterministic
}

func TestConcurrentUpdateDomains(t *testing.T) {
	m := NewManager(nil)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.UpdateDomains([]config.Domain{
				{Host: "example.com", Bandwidth: config.BandwidthConfig{Enabled: true, MonthlyLimit: 1000}},
			})
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Record("example.com", 10)
		}()
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestRecordZeroBytes(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	blocked, throttled := m.Record("example.com", 0)
	if blocked || throttled {
		t.Error("0 bytes should not block or throttle")
	}

	status := m.GetStatus("example.com")
	if status.MonthlyBytes != 0 {
		t.Errorf("expected MonthlyBytes=0, got %d", status.MonthlyBytes)
	}
}

func TestRecordExactlyAtLimit(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))

	// Exactly at limit — should be blocked (>= comparison)
	blocked, _ := m.Record("example.com", 100)
	if !blocked {
		t.Error("should be blocked at exactly the limit")
	}
}

func TestRecordExactlyAt80PctThrottle(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "throttle",
	}))

	// Exactly 80 bytes = 80% — should be throttled (>= comparison)
	_, throttled := m.Record("example.com", 80)
	if !throttled {
		t.Error("should be throttled at exactly 80%%")
	}
}

func TestMiddlewareWrapsResponseWriter(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100000,
	}))

	mw := m.Middleware()

	var capturedWriter http.ResponseWriter
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedWriter = w
		w.Write([]byte("test"))
	}))

	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	rw, ok := capturedWriter.(*responseWriter)
	if !ok {
		t.Fatal("expected wrapped responseWriter")
	}
	if rw.host != "example.com" {
		t.Errorf("expected host=example.com, got %s", rw.host)
	}
	if rw.bytesWritten != 4 {
		t.Errorf("expected bytesWritten=4, got %d", rw.bytesWritten)
	}
}

func TestHandlerAllStatusEmpty(t *testing.T) {
	m := NewManager(nil)

	allHandler, _ := m.Handler()

	req := httptest.NewRequest("GET", "/api/v1/bandwidth", nil)
	rec := httptest.NewRecorder()
	allHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Should return empty array or null, both are valid JSON
	body := rec.Body.String()
	if body != "null\n" && body != "[]\n" {
		t.Errorf("expected null or empty array, got %q", body)
	}
}

func TestRecordDailyLimitThrottle(t *testing.T) {
	// Test daily limit with throttle action (not block)
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
		Action:     "throttle",
	}))

	_, throttled := m.Record("example.com", 81)
	if !throttled {
		t.Error("should be throttled at 81%% of daily with throttle action")
	}
}

func TestRecordBlockMonthlyAndDaily(t *testing.T) {
	// Both limits set, monthly hit first
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		DailyLimit:   200,
		Action:       "block",
	}))

	blocked, _ := m.Record("example.com", 150)
	if !blocked {
		t.Error("should be blocked when monthly exceeded even if daily not exceeded")
	}
}

func TestRecordBlockDailyBeforeMonthly(t *testing.T) {
	// Daily limit lower than monthly
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 1000,
		DailyLimit:   50,
		Action:       "block",
	}))

	blocked, _ := m.Record("example.com", 60)
	if !blocked {
		t.Error("should be blocked when daily exceeded even if monthly not exceeded")
	}
}

func TestRecordThrottleMonthlyZeroDailySet(t *testing.T) {
	// Monthly limit is 0 (disabled), only daily limit for throttle
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:    true,
		DailyLimit: 100,
		Action:     "throttle",
	}))

	// 80% of 0 is 0, so monthly throttle threshold is 0 — but monthlyLimit is 0 so that branch is skipped
	_, throttled := m.Record("example.com", 50)
	if throttled {
		t.Error("should not be throttled at 50%% of daily")
	}

	_, throttled = m.Record("example.com", 31)
	if !throttled {
		t.Error("should be throttled at 81/100 daily")
	}
}

func TestResponseWriterFlushMultipleTimes(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100000,
	}))

	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, host: "example.com", manager: m}

	rw.Write([]byte("abc"))
	rw.Flush()
	// bytesWritten is still 3 — Flush doesn't reset it
	rw.Flush()

	status := m.GetStatus("example.com")
	if status == nil {
		t.Fatal("expected status")
	}
	// Called Record twice with 3 bytes each time
	if status.MonthlyBytes != 6 {
		t.Errorf("expected MonthlyBytes=6 after double flush, got %d", status.MonthlyBytes)
	}
}

func TestDomainUsageJSONRoundTrip(t *testing.T) {
	du := &DomainUsage{
		MonthlyBytes: *newAtomicInt64(1234),
		DailyBytes:   *newAtomicInt64(567),
		LastReset:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		DailyReset:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		LastUpdated:  time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
		Blocked:      true,
		Throttled:    false,
	}

	data, err := json.Marshal(du)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded DomainUsage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// MonthlyBytes and DailyBytes have json:"-" tags so they are not serialized.
	// Only Blocked, Throttled, and time fields should be compared.
	if decoded.Blocked != du.Blocked {
		t.Errorf("Blocked mismatch: %v vs %v", decoded.Blocked, du.Blocked)
	}
}

func TestStatusJSONRoundTrip(t *testing.T) {
	s := Status{
		Host:         "test.com",
		MonthlyBytes: 999,
		DailyBytes:   100,
		MonthlyLimit: 5000,
		DailyLimit:   1000,
		MonthlyPct:   19.98,
		DailyPct:     10.0,
		Blocked:      false,
		Throttled:    true,
		LastReset:    time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		DailyReset:   time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded Status
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Host != s.Host {
		t.Errorf("Host mismatch: %s vs %s", decoded.Host, s.Host)
	}
	if decoded.Throttled != s.Throttled {
		t.Errorf("Throttled mismatch: %v vs %v", decoded.Throttled, s.Throttled)
	}
	if decoded.MonthlyPct != s.MonthlyPct {
		t.Errorf("MonthlyPct mismatch: %f vs %f", decoded.MonthlyPct, s.MonthlyPct)
	}
}
