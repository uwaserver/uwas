package alerting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestDomainDown(t *testing.T) {
	a := New(true, "", testLogger())
	a.DomainDown("example.com")

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
}

func TestCertExpiry(t *testing.T) {
	a := New(true, "", testLogger())
	a.CertExpiry("example.com", 5)

	alerts := a.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Level != "warning" {
		t.Errorf("expected level warning, got %s", alerts[0].Level)
	}
	if alerts[0].Type != "cert_expiry" {
		t.Errorf("expected type cert_expiry, got %s", alerts[0].Type)
	}
}

func TestWebhookDelivery(t *testing.T) {
	var received Alert
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	a := New(true, ts.URL, testLogger())
	a.DomainDown("webhook-test.com")

	// Wait for async webhook delivery
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if received.Host != "webhook-test.com" {
		t.Errorf("expected webhook host webhook-test.com, got %s", received.Host)
	}
	if received.Type != "domain_down" {
		t.Errorf("expected type domain_down, got %s", received.Type)
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

func TestRateLimitAlert(t *testing.T) {
	a := New(true, "", testLogger())

	// Trigger more than 100 rate limits in quick succession
	for i := 0; i < 105; i++ {
		a.RecordRateLimit()
	}

	alerts := a.Alerts()
	found := false
	for _, alert := range alerts {
		if alert.Type == "rate_limit" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected rate_limit alert to be generated")
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
