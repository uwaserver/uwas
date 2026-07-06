package bandwidth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// ---------------------------------------------------------------------------
// IsBlocked
// ---------------------------------------------------------------------------

func TestIsBlockedUnknownDomain(t *testing.T) {
	m := NewManager(nil)
	if m.IsBlocked("missing.com") {
		t.Error("expected false for unknown domain")
	}
}

func TestIsBlockedFalseWhenUnderLimit(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 10000,
		Action:       "block",
	}))
	m.Record("example.com", 100)
	if m.IsBlocked("example.com") {
		t.Error("expected false when usage is under the limit")
	}
}

func TestIsBlockedTrueWhenExceeded(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))
	// Exceed the hard limit so Blocked is set.
	m.Record("example.com", 200)
	if !m.IsBlocked("example.com") {
		t.Error("expected true after monthly limit exceeded")
	}
}

func TestIsBlockedResetClearsState(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))
	m.Record("example.com", 200)
	if !m.IsBlocked("example.com") {
		t.Fatal("precondition: expected blocked after exceeding limit")
	}
	m.Reset("example.com")
	if m.IsBlocked("example.com") {
		t.Error("expected false after Reset clears blocked flag")
	}
}

// TestRecordNormalizesHost is the regression for the enforcement bypass: the
// live dispatch path records usage with the raw request Host, which may carry a
// port or mixed case. Record must normalize it so usage lands on the configured
// domain's counter instead of silently going unrecorded.
func TestRecordNormalizesHost(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))
	// Raw Host with a port and mixed case must still hit "example.com".
	m.Record("Example.COM:8443", 200)
	if !m.IsBlocked("example.com") {
		t.Error("usage recorded under a raw host:port was not attributed to the configured domain")
	}
}

// ---------------------------------------------------------------------------
// Middleware: host:port stripping branch
// ---------------------------------------------------------------------------

func TestMiddlewareStripsPortFromHost(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 10000,
		Action:       "block",
	}))

	mw := m.Middleware()
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("ok"))
	}))

	// Host carries an explicit port; the middleware must strip it so the
	// "example.com" limit matches and the request flows through.
	req := httptest.NewRequest("GET", "http://example.com:8443/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should have been called for host with port")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareStripsPortBlockedDomain(t *testing.T) {
	m := NewManager(testDomains(config.BandwidthConfig{
		Enabled:      true,
		MonthlyLimit: 100,
		Action:       "block",
	}))
	m.Record("example.com", 200) // block it

	mw := m.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called when blocked")
	}))

	// Port-bearing host must still resolve to the blocked "example.com".
	req := httptest.NewRequest("GET", "http://example.com:443/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}
