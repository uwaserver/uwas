package proxy

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- circuit.go: Allow (90.9%) ---
// Uncovered: default switch case (line 70). The switch handles
// Closed, Open, HalfOpen — the default is reached when state has
// an unexpected value. We can trigger it by setting state directly.

func TestCircuitBreakerAllowUnknownState(t *testing.T) {
	cb := NewCircuitBreaker(5, time.Second)
	// Set state to an undefined value (3)
	cb.state.Store(int32(3))

	if !cb.Allow() {
		t.Error("Allow should return true for unknown state")
	}
}

func TestCircuitBreakerAllowClosed(t *testing.T) {
	cb := NewCircuitBreaker(5, time.Second)
	if !cb.Allow() {
		t.Error("Allow should return true in closed state")
	}
}

func TestCircuitBreakerAllowOpenTimeoutTransitions(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	// Trip the breaker
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("expected circuit open after failure")
	}

	// Immediately — should not allow
	if cb.Allow() {
		t.Error("Allow should return false immediately after trip")
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Now the timeout has elapsed — Allow should try to transition to half-open
	if !cb.Allow() {
		t.Error("Allow should return true after timeout (transition to half-open)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Errorf("state = %v, want CircuitHalfOpen", cb.State())
	}
}

func TestCircuitBreakerAllowHalfOpenOnlyOneProbe(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	// First Allow transitions to half-open and claims the probe
	if !cb.Allow() {
		t.Error("first allow should succeed")
	}

	// Second Allow should be rejected (probe already taken)
	if cb.Allow() {
		t.Error("second allow should be rejected in half-open while probe is in flight")
	}
}

func TestCircuitBreakerHalfOpenProbeSucceeds(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	cb.Allow() // transition to half-open, claim probe
	cb.RecordSuccess()

	if cb.State() != CircuitClosed {
		t.Errorf("state = %v, want CircuitClosed after success in half-open", cb.State())
	}
}

// --- handler.go: generateTraceparent (69.2%) ---
// Uncovered: fallback when rand.Read fails (lines 492-504).

func TestGenerateTraceparentFormat(t *testing.T) {
	tp := generateTraceparent()
	// Format: 00-<32 hex chars>-<16 hex chars>-01
	if len(tp) != 55 {
		t.Errorf("expected length 55, got %d: %q", len(tp), tp)
	}
	if tp[:3] != "00-" {
		t.Errorf("expected prefix '00-', got %q", tp[:3])
	}
	if tp[52:] != "-01" {
		t.Errorf("expected suffix '-01', got %q", tp[52:])
	}
	// Verify trace-id (bytes 3-34) is 32 hex chars
	for i := 3; i < 35; i++ {
		if !isHexChar(tp[i]) {
			t.Errorf("non-hex char at position %d: %q", i, tp[i])
		}
	}
	if tp[35] != '-' {
		t.Errorf("expected dash at 35, got %q", tp[35])
	}
	// Verify span-id (bytes 36-51) is 16 hex chars
	for i := 36; i < 52; i++ {
		if !isHexChar(tp[i]) {
			t.Errorf("non-hex char at position %d: %q", i, tp[i])
		}
	}
}

func isHexChar(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func TestGenerateTraceparentUnique(t *testing.T) {
	tp1 := generateTraceparent()
	tp2 := generateTraceparent()
	if tp1 == tp2 {
		t.Error("expected different traceparent values")
	}
}

// --- health.go: Start (85.7%) ---
// Uncovered: already running check (line 74-77)

func TestHealthCheckerStartAlreadyRunning(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
	})
	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/health",
		Interval:  100 * time.Millisecond,
		Timeout:   50 * time.Millisecond,
		Threshold: 1,
		Rise:      1,
	}, logger.New("error", "text"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// First start should succeed
	hc.Start(ctx)

	// Second start should be a no-op (already running)
	hc.Start(ctx)

	// Cleanup
	hc.Stop()
}

func TestHealthCheckerStartAndStop(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
	})
	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/health",
		Interval:  1 * time.Hour,
		Timeout:   50 * time.Millisecond,
		Threshold: 1,
		Rise:      1,
	}, logger.New("error", "text"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hc.Start(ctx)
	hc.Stop()
}

func TestHealthCheckerStopMultiple(t *testing.T) {
	pool := NewUpstreamPool(nil)
	hc := NewHealthChecker(pool, HealthConfig{}, logger.New("error", "text"))

	// Stop before Start — should be a no-op
	hc.Stop()
	hc.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hc.Start(ctx)
	hc.Stop()
	hc.Stop()
}

// --- mirror.go: doMirror (92.3%) ---
// Uncovered: validateBackendURL error (line 91-94)

func TestMirrorDoMirrorSSRFBlocked(t *testing.T) {
	log := logger.New("error", "text")
	m := NewMirror(MirrorConfig{
		Enabled:               true,
		Backend:               "http://169.254.169.254",
		Percent:               100,
		AllowPrivateUpstreams: false,
	}, log)

	req := httptest.NewRequest("GET", "/", nil)

	// This should not panic — SSRF check will reject the URL and log a warning
	m.doMirror(req, nil)
}

func TestMirrorDoMirrorRequestCreationError(t *testing.T) {
	log := logger.New("error", "text")
	m := NewMirror(MirrorConfig{
		Enabled:               true,
		Backend:               "http://127.0.0.1:9999",
		Percent:               100,
		AllowPrivateUpstreams: true,
	}, log)

	req := httptest.NewRequest("GET", "/", nil)
	m.doMirror(req, []byte("test body"))
}


func TestBackendHealthStates(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
		{Address: "http://127.0.0.1:2", Weight: 1},
	})
	backends := pool.All()
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(backends))
	}

	if !backends[0].IsHealthy() {
		t.Error("initial state should be healthy")
	}
	if backends[0].GetState() != StateHealthy {
		t.Errorf("state = %v, want StateHealthy", backends[0].GetState())
	}

	backends[0].SetState(StateUnhealthy)
	if backends[0].IsHealthy() {
		t.Error("should be unhealthy after SetState(StateUnhealthy)")
	}

	backends[0].SetState(StateDraining)
	if backends[0].IsHealthy() {
		t.Error("should not be healthy in draining state")
	}
}

// --- handler.go: classifyUpstreamErr, isTimeoutErr, isRetryableError ---

func TestClassifyUpstreamErrNil(t *testing.T) {
	if s := classifyUpstreamErr(nil); s != "no upstream error" {
		t.Errorf("expected 'no upstream error', got %q", s)
	}
}

func TestIsTimeoutErrChecks(t *testing.T) {
	if isTimeoutErr(nil) {
		t.Error("nil should not be a timeout error")
	}

	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	<-ctx.Done()

	if !isTimeoutErr(ctx.Err()) {
		t.Error("context.DeadlineExceeded should be a timeout error")
	}
}

func TestRetryableErrorNil(t *testing.T) {
	if isRetryableError(nil) {
		t.Error("nil should not be retryable")
	}
}
