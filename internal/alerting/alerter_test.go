package alerting

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

func testLogger() *logger.Logger {
	return logger.New("error", "text")
}

func TestAlertRecordAndRetrieve(t *testing.T) {
	a := New(true, "", testLogger())

	a.Alert(Alert{
		Level:   "critical",
		Type:    "domain_down",
		Host:    "example.com",
		Message: "Domain example.com is down",
	})

	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Level != "critical" {
		t.Errorf("expected level critical, got %s", alerts[0].Level)
	}
	if alerts[0].Type != "domain_down" {
		t.Errorf("expected type domain_down, got %s", alerts[0].Type)
	}
	if alerts[0].Host != "example.com" {
		t.Errorf("expected host example.com, got %s", alerts[0].Host)
	}
}

func TestAlertRingBuffer(t *testing.T) {
	a := New(true, "", testLogger())

	// Fill more than maxAlertHistory alerts
	for i := 0; i < maxAlertHistory+20; i++ {
		a.Alert(Alert{
			Level:   "info",
			Type:    "rate_limit",
			Host:    "test.com",
			Message: "alert " + itoa(i),
		})
	}

	alerts := a.Alerts()
	if len(alerts) != maxAlertHistory {
		t.Fatalf("expected %d alerts, got %d", maxAlertHistory, len(alerts))
	}

	// Most recent should be last one we inserted
	if alerts[0].Message != "alert "+itoa(maxAlertHistory+19) {
		t.Errorf("expected most recent alert, got %s", alerts[0].Message)
	}
}

func TestAlertDisabled(t *testing.T) {
	a := New(false, "", testLogger())

	a.Alert(Alert{
		Level:   "critical",
		Type:    "domain_down",
		Host:    "example.com",
		Message: "should not be recorded",
	})

	alerts := a.Alerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts when disabled, got %d", len(alerts))
	}
}

func TestErrorSpikeDetection(t *testing.T) {
	a := New(true, "", testLogger())

	// Record 5 OK requests, then 10 errors to push past 10% threshold
	for i := 0; i < 5; i++ {
		a.RecordRequest(false)
	}
	for i := 0; i < 10; i++ {
		a.RecordRequest(true)
	}

	alerts := a.Alerts()
	found := false
	for _, alert := range alerts {
		if alert.Type == "error_spike" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected error_spike alert to be generated")
	}
}

func TestEmptyAlerts(t *testing.T) {
	a := New(true, "", testLogger())
	alerts := a.Alerts()
	if alerts != nil {
		t.Errorf("expected nil alerts on fresh alerter, got %v", alerts)
	}
}

func TestItoaFtoa(t *testing.T) {
	if got := itoa(42); got != "42" {
		t.Errorf("itoa(42) = %q, want %q", got, "42")
	}
	if got := itoa(0); got != "0" {
		t.Errorf("itoa(0) = %q, want %q", got, "0")
	}
	if got := ftoa(12.3); got != "12.3" {
		t.Errorf("ftoa(12.3) = %q, want %q", got, "12.3")
	}
}

func TestConcurrentAlerts(t *testing.T) {
	a := New(true, "", testLogger())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			a.Alert(Alert{
				Level:   "info",
				Type:    "rate_limit",
				Host:    "test.com",
				Message: "concurrent " + itoa(n),
			})
		}(i)
	}
	wg.Wait()

	alerts := a.Alerts()
	if len(alerts) != 50 {
		t.Errorf("expected 50 alerts, got %d", len(alerts))
	}
}

func TestEmptyWebhookURL(t *testing.T) {
	// With empty webhook URL, no HTTP call should be made (no crash)
	a := New(true, "", testLogger())
	a.Alert(Alert{
		Level:   "info",
		Type:    "rate_limit",
		Host:    "test.com",
		Message: "no webhook",
	})

	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}

func TestAlertHistoryRingBufferWrap(t *testing.T) {
	a := New(true, "", testLogger())

	// Fill exactly maxAlertHistory alerts
	for i := 0; i < maxAlertHistory; i++ {
		a.Alert(Alert{
			Level:   "info",
			Type:    "rate_limit",
			Host:    "wrap.com",
			Message: "msg " + itoa(i),
		})
	}

	alerts := a.Alerts()
	if len(alerts) != maxAlertHistory {
		t.Fatalf("expected %d alerts, got %d", maxAlertHistory, len(alerts))
	}

	// Newest should be last one added
	if alerts[0].Message != "msg "+itoa(maxAlertHistory-1) {
		t.Errorf("most recent = %q, want %q", alerts[0].Message, "msg "+itoa(maxAlertHistory-1))
	}

	// Oldest should be first one added
	if alerts[maxAlertHistory-1].Message != "msg 0" {
		t.Errorf("oldest = %q, want %q", alerts[maxAlertHistory-1].Message, "msg 0")
	}

	// Now add one more to wrap
	a.Alert(Alert{
		Level:   "info",
		Type:    "rate_limit",
		Host:    "wrap.com",
		Message: "msg " + itoa(maxAlertHistory),
	})

	alerts = a.Alerts()
	if len(alerts) != maxAlertHistory {
		t.Fatalf("expected %d alerts after wrap, got %d", maxAlertHistory, len(alerts))
	}

	// Newest should be the one we just added
	if alerts[0].Message != "msg "+itoa(maxAlertHistory) {
		t.Errorf("newest after wrap = %q", alerts[0].Message)
	}

	// The oldest (msg 0) should have been overwritten, oldest now is msg 1
	if alerts[maxAlertHistory-1].Message != "msg 1" {
		t.Errorf("oldest after wrap = %q, want 'msg 1'", alerts[maxAlertHistory-1].Message)
	}
}

func TestConcurrentAlertAndRetrieve(t *testing.T) {
	a := New(true, "", testLogger())
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			a.Alert(Alert{
				Level:   "info",
				Type:    "rate_limit",
				Host:    "concurrent.com",
				Message: "msg " + itoa(n),
			})
		}(i)
	}

	// Concurrent reads while writes happen
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.Alerts()
		}()
	}

	wg.Wait()

	alerts := a.Alerts()
	if len(alerts) != maxAlertHistory {
		t.Errorf("expected %d alerts, got %d", maxAlertHistory, len(alerts))
	}
}

func TestAlertTimestampAutoSet(t *testing.T) {
	a := New(true, "", testLogger())
	before := time.Now()
	a.Alert(Alert{
		Level:   "info",
		Type:    "rate_limit",
		Message: "auto time",
	})
	after := time.Now()

	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Time.Before(before) || alerts[0].Time.After(after) {
		t.Errorf("auto-set time %v not between %v and %v", alerts[0].Time, before, after)
	}
}

func TestAlertTimestampExplicit(t *testing.T) {
	a := New(true, "", testLogger())
	explicit := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	a.Alert(Alert{
		Time:    explicit,
		Level:   "info",
		Type:    "rate_limit",
		Message: "explicit time",
	})

	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if !alerts[0].Time.Equal(explicit) {
		t.Errorf("time = %v, want %v", alerts[0].Time, explicit)
	}
}

func TestRecordRequestDisabled(t *testing.T) {
	a := New(false, "", testLogger())

	for i := 0; i < 20; i++ {
		a.RecordRequest(true)
	}

	alerts := a.Alerts()
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts when disabled, got %d", len(alerts))
	}
}

func TestItoaNegative(t *testing.T) {
	if got := itoa(-5); got != "-5" {
		t.Errorf("itoa(-5) = %q, want %q", got, "-5")
	}
	if got := itoa(-100); got != "-100" {
		t.Errorf("itoa(-100) = %q, want %q", got, "-100")
	}
}

func TestFtoaSmallValues(t *testing.T) {
	if got := ftoa(0.0); got != "0.0" {
		t.Errorf("ftoa(0.0) = %q, want %q", got, "0.0")
	}
	if got := ftoa(1.5); got != "1.5" {
		t.Errorf("ftoa(1.5) = %q, want %q", got, "1.5")
	}
	if got := ftoa(99.9); got != "99.9" {
		t.Errorf("ftoa(99.9) = %q, want %q", got, "99.9")
	}
}

func TestErrorSpikeBelowThreshold(t *testing.T) {
	a := New(true, "", testLogger())

	// Record 9 ok, 1 error = 10% exactly, should not trigger (needs > 10%)
	for i := 0; i < 9; i++ {
		a.RecordRequest(false)
	}
	a.RecordRequest(true)

	alerts := a.Alerts()
	for _, alert := range alerts {
		if alert.Type == "error_spike" {
			t.Error("should not trigger error_spike at exactly 10%")
		}
	}
}

func TestErrorSpikeTooFewSamples(t *testing.T) {
	a := New(true, "", testLogger())

	// Only 5 errors with no OKs = 100% error rate, but < 10 samples
	for i := 0; i < 5; i++ {
		a.RecordRequest(true)
	}

	alerts := a.Alerts()
	for _, alert := range alerts {
		if alert.Type == "error_spike" {
			t.Error("should not trigger error_spike with < 10 samples")
		}
	}
}

func TestErrorSpikeDedup(t *testing.T) {
	// After an error_spike alert fires, a second one should not fire within 1 minute
	a := New(true, "", testLogger())

	// Trigger error spike: 5 OK + 10 errors (total 15, error rate ~66%)
	for i := 0; i < 5; i++ {
		a.RecordRequest(false)
	}
	for i := 0; i < 10; i++ {
		a.RecordRequest(true)
	}

	// Count error_spike alerts
	alerts := a.Alerts()
	spikeCount := 0
	for _, alert := range alerts {
		if alert.Type == "error_spike" {
			spikeCount++
		}
	}
	if spikeCount != 1 {
		t.Fatalf("expected 1 error_spike alert, got %d", spikeCount)
	}

	// Now add more errors -- should NOT fire another alert within the same minute
	for i := 0; i < 10; i++ {
		a.RecordRequest(true)
	}

	alerts = a.Alerts()
	spikeCount = 0
	for _, alert := range alerts {
		if alert.Type == "error_spike" {
			spikeCount++
		}
	}
	if spikeCount != 1 {
		t.Errorf("expected still 1 error_spike (dedup), got %d", spikeCount)
	}
}

func TestFtoaNegativeFraction(t *testing.T) {
	// ftoa with negative value triggers frac < 0 branch
	got := ftoa(-3.7)
	if got != "-3.7" {
		t.Errorf("ftoa(-3.7) = %q, want %q", got, "-3.7")
	}
}

func TestRecordRequestPrunesOldEntries(t *testing.T) {
	a := New(true, "", testLogger())

	// Manually insert old entries
	a.errorWindowMu.Lock()
	oldTime := time.Now().Add(-10 * time.Minute)
	for i := 0; i < 20; i++ {
		a.errorWindow = append(a.errorWindow, errorEntry{time: oldTime, isErr: true})
	}
	a.errorWindowMu.Unlock()

	// Now record a new request -- should prune all old entries
	a.RecordRequest(false)

	a.errorWindowMu.Lock()
	count := len(a.errorWindow)
	a.errorWindowMu.Unlock()

	// Should only have the 1 new entry (all old ones pruned)
	if count != 1 {
		t.Errorf("after pruning, error window has %d entries, want 1", count)
	}
}

// --- Webhook delivery tests ---

func TestWebhookDeliveryViaAlert(t *testing.T) {
	var mu sync.Mutex
	var received []byte
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = body
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	a := New(true, srv.URL, testLogger())
	a.Alert(Alert{Level: "warning", Type: "test_alert", Host: "webhook.com", Message: "test delivery"})

	// Wait for the async webhook goroutine to complete
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("webhook did not receive any payload")
	}
	if !strings.Contains(string(received), "test_alert") {
		t.Errorf("webhook payload missing alert type: %s", string(received))
	}
}

func TestWebhookDeliveryFailure(t *testing.T) {
	// Point to a non-existent server — use a short timeout so test doesn't hang
	a := New(true, "http://127.0.0.1:1/nonexistent", testLogger())
	a.client.Timeout = 500 * time.Millisecond
	// Should not panic
	a.Alert(Alert{Level: "critical", Type: "fail_test", Message: "should not crash"})

	// Poll until alert is recorded (the webhook goroutine will fail fast on connection refused)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.Alerts()) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}

func TestWebhookErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // server error
	}))
	defer srv.Close()

	a := New(true, srv.URL, testLogger())
	a.Alert(Alert{Level: "info", Type: "status_test", Message: "server returns 500"})
	time.Sleep(100 * time.Millisecond)

	// Alert should still be recorded
	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
}
