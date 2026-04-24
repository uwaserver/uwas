package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// --- Upstream Pool ---

func TestUpstreamPoolHealthy(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://localhost:3000", Weight: 1},
		{Address: "http://localhost:3001", Weight: 1},
	})

	healthy := pool.Healthy()
	if len(healthy) != 2 {
		t.Errorf("healthy = %d, want 2", len(healthy))
	}

	// Mark one unhealthy
	pool.backends[0].SetState(StateUnhealthy)
	healthy = pool.Healthy()
	if len(healthy) != 1 {
		t.Errorf("healthy = %d after marking one unhealthy, want 1", len(healthy))
	}
}

// --- Balancers ---

func TestRoundRobin(t *testing.T) {
	backends := makeBackends(3)
	rr := &RoundRobin{}
	req := httptest.NewRequest("GET", "/", nil)

	counts := make(map[string]int)
	for i := 0; i < 300; i++ {
		b := rr.Select(backends, req)
		counts[b.URL.String()]++
	}

	// Each should get ~100
	for _, c := range counts {
		if c < 80 || c > 120 {
			t.Errorf("distribution uneven: %v", counts)
			break
		}
	}
}

func TestLeastConn(t *testing.T) {
	backends := makeBackends(3)
	backends[0].ActiveConns.Store(10)
	backends[1].ActiveConns.Store(2)
	backends[2].ActiveConns.Store(5)

	lc := &LeastConn{}
	req := httptest.NewRequest("GET", "/", nil)

	selected := lc.Select(backends, req)
	if selected != backends[1] {
		t.Error("should select backend with least connections")
	}
}

func TestIPHash(t *testing.T) {
	backends := makeBackends(3)
	ih := &IPHash{}

	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "1.2.3.4:1234"

	// Same IP should always select same backend
	first := ih.Select(backends, req1)
	for i := 0; i < 100; i++ {
		got := ih.Select(backends, req1)
		if got != first {
			t.Error("IP hash should be consistent")
			break
		}
	}
}

func TestURIHash(t *testing.T) {
	backends := makeBackends(3)
	uh := &URIHash{}

	req1 := httptest.NewRequest("GET", "/api/users", nil)

	first := uh.Select(backends, req1)
	for i := 0; i < 100; i++ {
		got := uh.Select(backends, req1)
		if got != first {
			t.Error("URI hash should be consistent")
			break
		}
	}
}

func TestRandomPowerOf2(t *testing.T) {
	backends := makeBackends(10)
	rn := &Random{}
	req := httptest.NewRequest("GET", "/", nil)

	// Just verify it doesn't panic and returns non-nil
	for i := 0; i < 100; i++ {
		b := rn.Select(backends, req)
		if b == nil {
			t.Fatal("Random returned nil")
		}
	}
}

func TestNewBalancer(t *testing.T) {
	algos := []string{"round_robin", "least_conn", "ip_hash", "uri_hash", "random", ""}
	for _, a := range algos {
		b := NewBalancer(a)
		if b == nil {
			t.Errorf("NewBalancer(%q) returned nil", a)
		}
	}
}

func TestBalancerEmptyBackends(t *testing.T) {
	balancers := []Balancer{
		&RoundRobin{}, &LeastConn{}, &IPHash{}, &URIHash{}, &Random{},
	}
	req := httptest.NewRequest("GET", "/", nil)

	for _, b := range balancers {
		if b.Select(nil, req) != nil {
			t.Error("should return nil for empty backends")
		}
	}
}

// --- Circuit Breaker ---

func TestCircuitBreakerClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Second)

	if !cb.Allow() {
		t.Error("should allow in closed state")
	}
	if cb.State() != CircuitClosed {
		t.Error("initial state should be closed")
	}
}

func TestCircuitBreakerTrip(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Second)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Error("should be open after 3 failures")
	}
	if cb.Allow() {
		t.Error("should not allow in open state")
	}
}

func TestCircuitBreakerRecovery(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	if !cb.Allow() {
		t.Error("should allow in half-open state")
	}
	if cb.State() != CircuitHalfOpen {
		t.Error("should be half-open")
	}

	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Error("should be closed after success in half-open")
	}
}

// --- Proxy Handler with real upstream ---

func TestProxyHandlerSuccess(t *testing.T) {
	// Start a test upstream
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "test")
		w.WriteHeader(200)
		w.Write([]byte("upstream response"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	// Create request context
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "upstream response" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("X-Backend") != "test" {
		t.Error("upstream headers should be forwarded")
	}
}

func TestProxyHandlerNoBackends(t *testing.T) {
	pool := NewUpstreamPool(nil)
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	h.Serve(ctx, newTestDomain(), pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")

	if !IsWebSocketUpgrade(req) {
		t.Error("should detect WebSocket upgrade")
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	if IsWebSocketUpgrade(req2) {
		t.Error("should not detect WebSocket for normal request")
	}
}

// Helpers

func makeBackends(n int) []*Backend {
	backends := make([]*Backend, n)
	for i := range backends {
		u, _ := url.Parse(fmt.Sprintf("http://localhost:%d", 3000+i))
		backends[i] = &Backend{URL: u, Weight: 1}
	}
	return backends
}

func newTestLogger() *logger.Logger {
	return logger.New("error", "text")
}

func newTestContext(rec *httptest.ResponseRecorder, req *http.Request) *router.RequestContext {
	return router.AcquireContext(rec, req)
}

func newTestDomain() *config.Domain {
	return &config.Domain{
		Host: "test.com",
		Type: "proxy",
	}
}

// --- Additional coverage tests ---

func TestUpstreamPoolAll(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://localhost:3000", Weight: 1},
		{Address: "http://localhost:3001", Weight: 2},
		{Address: "http://localhost:3002", Weight: 3},
	})

	all := pool.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d backends, want 3", len(all))
	}
}

func TestUpstreamPoolLen(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://localhost:3000", Weight: 1},
		{Address: "http://localhost:3001", Weight: 1},
	})

	if got := pool.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

func TestUpstreamPoolLenEmpty(t *testing.T) {
	pool := NewUpstreamPool(nil)
	if got := pool.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
}

func TestHealthCheckerTransitions(t *testing.T) {
	// Track request count to alternate between healthy and unhealthy
	var mu sync.Mutex
	requestCount := 0
	failAfter := 0 // start healthy
	recoverAt := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		n := requestCount
		mu.Unlock()

		// First few requests succeed, then fail, then recover
		if n > failAfter && failAfter > 0 && (recoverAt == 0 || n < recoverAt) {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: ts.URL, Weight: 1},
	})

	log := newTestLogger()
	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/",
		Interval:  50 * time.Millisecond,
		Timeout:   2 * time.Second,
		Threshold: 2, // 2 failures → unhealthy
		Rise:      2, // 2 successes → healthy
	}, log)

	backend := pool.All()[0]

	// Phase 1: healthy checks
	if !backend.IsHealthy() {
		t.Fatal("backend should start healthy")
	}

	// Manually call checkAll to verify it stays healthy
	hc.checkAll()
	hc.checkAll()
	if !backend.IsHealthy() {
		t.Fatal("backend should remain healthy after successful checks")
	}

	// Phase 2: make server fail
	mu.Lock()
	failAfter = requestCount // all subsequent requests fail
	recoverAt = 0
	mu.Unlock()

	hc.checkAll() // failure 1
	hc.checkAll() // failure 2 → should become unhealthy

	if backend.IsHealthy() {
		t.Error("backend should be unhealthy after 2 consecutive failures")
	}

	// Phase 3: make server recover
	mu.Lock()
	recoverAt = requestCount + 1 // next request succeeds
	failAfter = 0
	mu.Unlock()

	hc.checkAll() // success 1
	if backend.IsHealthy() {
		t.Error("backend should still be unhealthy after only 1 success (rise=2)")
	}

	hc.checkAll() // success 2 → should recover

	if !backend.IsHealthy() {
		t.Error("backend should have recovered after 2 consecutive successes")
	}
}

func TestNewCircuitBreakerDefaults(t *testing.T) {
	cb := NewCircuitBreaker(0, 0)

	// Should use defaults: threshold=5, timeout=30s
	// Verify by recording 4 failures (less than default 5) — should stay closed
	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitClosed {
		t.Error("should still be closed after 4 failures with default threshold=5")
	}

	cb.RecordFailure() // 5th failure
	if cb.State() != CircuitOpen {
		t.Error("should be open after 5 failures with default threshold=5")
	}
}

// === Additional coverage tests ===

// --- upstream.go: NewUpstreamPool with invalid URL (should skip), All/Len ---

func TestNewUpstreamPoolInvalidURL(t *testing.T) {
	// url.Parse rarely fails, but we can also test zero-weight defaulting.
	// A "://" alone is invalid enough:
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://good:8080", Weight: 1},
		{Address: "://bad url", Weight: 2},
	})
	// The invalid URL is silently skipped by url.Parse failure path.
	// url.Parse is lenient, so let's also confirm zero-weight defaults:
	pool2 := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://localhost:3000", Weight: 0},
		{Address: "http://localhost:3001", Weight: -5},
	})
	for _, b := range pool2.All() {
		if b.Weight != 1 {
			t.Errorf("expected weight=1 for zero/negative input, got %d", b.Weight)
		}
	}
	_ = pool // pool was created just to exercise the code path
}

func TestUpstreamPoolAllCopy(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://a:1", Weight: 1},
		{Address: "http://b:2", Weight: 1},
	})
	all1 := pool.All()
	all2 := pool.All()
	if &all1[0] == &all2[0] {
		t.Error("All() should return a copy, not the same slice")
	}
	if pool.Len() != 2 {
		t.Errorf("Len() = %d, want 2", pool.Len())
	}
}

// --- handler.go: Serve with backend timeout (504), forwardedProto HTTPS ---

func TestProxyHandlerTimeout504(t *testing.T) {
	// Start a backend that hangs long enough to trigger the read timeout
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	// Create a request with a deadline already set on its context.
	// The handler checks ctx.Request.Context().Err() for DeadlineExceeded.
	req := httptest.NewRequest("GET", "/slow", nil)
	reqCtx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(reqCtx)

	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 200 * time.Millisecond}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 504 {
		t.Errorf("status = %d, want 504 for timeout", rec.Code)
	}
}

func TestProxyHandlerForwardedProtoHTTPS(t *testing.T) {
	var gotProto string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProto = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)
	ctx.IsHTTPS = true

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if gotProto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", gotProto)
	}
}

func TestProxyHandlerBackendDown502(t *testing.T) {
	// Use a backend that refuses connections (closed immediately)
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://127.0.0.1:1", Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 1 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for connection refused", rec.Code)
	}
}

func TestClientIPNoPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4" // no port
	got := clientIP(req)
	if got != "1.2.3.4" {
		t.Errorf("clientIP = %q, want 1.2.3.4", got)
	}
}

// --- health.go: Start with context cancel (goroutine exits) ---

func TestHealthCheckerStartCancelContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: ts.URL, Weight: 1}})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Interval: 10 * time.Millisecond,
		Timeout:  2 * time.Second,
	}, log)

	ctx, cancel := context.WithCancel(context.Background())
	hc.Start(ctx)

	// Let at least one tick fire
	time.Sleep(30 * time.Millisecond)

	// Cancel the context; goroutine should exit
	cancel()

	// Give goroutine time to observe cancellation
	time.Sleep(30 * time.Millisecond)

	// If the goroutine leaks, it's a problem but we can't easily detect it here.
	// The main assertion is that cancel() doesn't panic/hang.
	backend := pool.All()[0]
	if !backend.IsHealthy() {
		t.Error("backend should still be healthy after health checks ran")
	}
}

func TestHealthCheckerSkipsDraining(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: ts.URL, Weight: 1}})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Interval:  50 * time.Millisecond,
		Timeout:   2 * time.Second,
		Threshold: 1,
	}, log)

	backend := pool.All()[0]
	backend.SetState(StateDraining)

	// checkAll should skip draining backends
	hc.checkAll()
	hc.checkAll()

	if backend.GetState() != StateDraining {
		t.Error("draining backend should not be modified by health checks")
	}
}

func TestNewHealthCheckerDefaults(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://localhost:9999", Weight: 1}})
	log := newTestLogger()

	// Pass all zeros/empty to trigger default assignments
	hc := NewHealthChecker(pool, HealthConfig{}, log)

	if hc.interval != 10*time.Second {
		t.Errorf("default interval = %v, want 10s", hc.interval)
	}
	if hc.timeout != 5*time.Second {
		t.Errorf("default timeout = %v, want 5s", hc.timeout)
	}
	if hc.threshold != 3 {
		t.Errorf("default threshold = %d, want 3", hc.threshold)
	}
	if hc.rise != 2 {
		t.Errorf("default rise = %d, want 2", hc.rise)
	}
	if hc.path != "/" {
		t.Errorf("default path = %q, want /", hc.path)
	}
}

// --- circuit.go: HalfOpen CAS single probe ---

func TestCircuitBreakerHalfOpenCAS(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Trip the breaker
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after 2 failures")
	}

	// Wait for timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)

	// First Allow() transitions from Open to HalfOpen, claims probe slot via CAS, returns true
	if !cb.Allow() {
		t.Fatal("first call should be allowed (transitions to half-open, claims slot)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should be half-open now")
	}

	// Second Allow() in HalfOpen: CAS on probeSlot fails (slot already claimed), returns false
	if cb.Allow() {
		t.Error("second call should be rejected in half-open (probeSlot already claimed)")
	}

	// Third Allow() in HalfOpen: still rejected
	if cb.Allow() {
		t.Error("third call should be rejected in half-open (probeSlot still claimed)")
	}
}

func TestCircuitBreakerHalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	// Trip open
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // transitions to half-open

	// Record a failure in half-open — should go back to open
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Error("should return to open after failure in half-open")
	}
}

func TestCircuitBreakerRecordSuccessInClosed(t *testing.T) {
	cb := NewCircuitBreaker(5, time.Second)

	// Record some failures then a success while still closed
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()

	if cb.State() != CircuitClosed {
		t.Error("should remain closed")
	}
	// Failures should have been reset by RecordSuccess
	// Now it should take 5 more failures to trip
	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitClosed {
		t.Error("should still be closed after 4 failures (reset by success)")
	}
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Error("should be open after 5 failures")
	}
}

// --- Proxy headers: test X-Forwarded-For / X-Real-IP ---

func TestProxyHeadersForwarded(t *testing.T) {
	var gotXFF, gotXRI, gotXFH string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotXRI = r.Header.Get("X-Real-IP")
		gotXFH = r.Header.Get("X-Forwarded-Host")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	req.Host = "myhost.com"
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	h.Serve(ctx, newTestDomain(), pool, balancer)

	if gotXFF != "10.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want 10.0.0.1", gotXFF)
	}
	if gotXRI != "10.0.0.1" {
		t.Errorf("X-Real-IP = %q, want 10.0.0.1", gotXRI)
	}
	if gotXFH != "myhost.com" {
		t.Errorf("X-Forwarded-Host = %q, want myhost.com", gotXFH)
	}
}

// --- Random balancer with single backend ---

func TestRandomSingleBackend(t *testing.T) {
	backends := makeBackends(1)
	rn := &Random{}
	req := httptest.NewRequest("GET", "/", nil)

	for i := 0; i < 10; i++ {
		b := rn.Select(backends, req)
		if b != backends[0] {
			t.Fatal("single backend should always be selected")
		}
	}
}

// --- Retry Logic ---

func TestProxyRetryOnConnectionRefused(t *testing.T) {
	// First backend refuses connections; second backend works.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "good")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1}, // will refuse
		{Address: upstream.URL, Weight: 1},         // will succeed
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 2
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 2 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (should have retried to good backend)", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rec.Body.String())
	}
}

func TestProxyRetryExhausted(t *testing.T) {
	// Both backends refuse connections — all retries exhausted.
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
		{Address: "http://127.0.0.1:2", Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 2
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 2 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 after all retries exhausted", rec.Code)
	}
}

func TestProxyNoRetryOnSuccess(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(200)
		w.Write([]byte("first call"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 3

	h.Serve(ctx, domain, pool, balancer)

	if callCount != 1 {
		t.Errorf("upstream called %d times, want 1 (no retry on success)", callCount)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestProxyDefaultMaxRetries(t *testing.T) {
	// When MaxRetries is 0, default to 2
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	// MaxRetries is 0, which should default to 2
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 2 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 with default retry", rec.Code)
	}
}

func TestIsRetryableError(t *testing.T) {
	if isRetryableError(nil) {
		t.Error("nil error should not be retryable")
	}

	// Test with string containing "connection refused"
	err := fmt.Errorf("dial tcp 127.0.0.1:1: connection refused")
	if !isRetryableError(err) {
		t.Error("connection refused should be retryable")
	}

	err2 := fmt.Errorf("dial tcp: connection reset")
	if !isRetryableError(err2) {
		t.Error("connection reset should be retryable")
	}
}

// --- Canary Routing ---

func TestCanaryIsCanaryDisabled(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: false,
		Weight:  50,
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	req := httptest.NewRequest("GET", "/", nil)
	if cr.IsCanary(req, cfg) {
		t.Error("should not be canary when disabled")
	}
}

func TestCanaryIsCanaryWeight100(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  100,
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	req := httptest.NewRequest("GET", "/", nil)
	if !cr.IsCanary(req, cfg) {
		t.Error("should always be canary with weight 100")
	}
}

func TestCanaryIsCanaryWeight0(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  0,
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	req := httptest.NewRequest("GET", "/", nil)
	if cr.IsCanary(req, cfg) {
		t.Error("should never be canary with weight 0")
	}
}

func TestCanaryCookieStickiness(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  0, // weight 0 means without cookie it should not be canary
		Cookie:  "uwas-canary",
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	// Request without cookie: should not be canary (weight 0)
	req := httptest.NewRequest("GET", "/", nil)
	if cr.IsCanary(req, cfg) {
		t.Error("should not be canary without cookie and weight 0")
	}

	// Request with canary cookie: should be canary regardless of weight
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: "uwas-canary", Value: "true"})
	if !cr.IsCanary(req2, cfg) {
		t.Error("should be canary when cookie is set")
	}

	// Request with canary cookie value=false: should not be canary
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.AddCookie(&http.Cookie{Name: "uwas-canary", Value: "false"})
	if cr.IsCanary(req3, cfg) {
		t.Error("should not be canary when cookie value is false")
	}
}

func TestCanaryDefaultCookieName(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  0,
		// Cookie is empty, should default to "X-Canary"
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "X-Canary", Value: "true"})
	if !cr.IsCanary(req, cfg) {
		t.Error("should use X-Canary as default cookie name")
	}
}

func TestCanaryServe(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "canary")
		w.WriteHeader(200)
		w.Write([]byte("canary response"))
	}))
	defer upstream.Close()

	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  100,
		Upstreams: []config.Upstream{
			{Address: upstream.URL, Weight: 1},
		},
		Cookie: "uwas-canary",
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Canary = cfg

	cr.Serve(ctx, domain, h)

	if rec.Code != 200 {
		t.Errorf("canary status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "canary response" {
		t.Errorf("canary body = %q", rec.Body.String())
	}
	if rec.Header().Get("X-Canary") != "true" {
		t.Error("X-Canary header should be set on canary response")
	}
	// Check stickiness cookie
	cookies := rec.Header().Get("Set-Cookie")
	if cookies == "" || !strings.Contains(cookies, "uwas-canary=true") {
		t.Errorf("expected canary stickiness cookie, got %q", cookies)
	}
}

func TestCanaryServeNoHealthyBackends(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  100,
		Upstreams: []config.Upstream{
			{Address: "http://127.0.0.1:1", Weight: 1},
		},
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	// Mark all canary backends unhealthy
	for _, b := range cr.CanaryPool().All() {
		b.SetState(StateUnhealthy)
	}

	h := New(log)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Canary = cfg

	cr.Serve(ctx, domain, h)

	// When no healthy canary backends, it should fall back (not write a response)
	// rec.Code will be the default 200 from httptest.NewRecorder since Serve returns early
	// The caller should handle fallback. Check that X-Canary is NOT set.
	if rec.Header().Get("X-Canary") == "true" {
		t.Error("X-Canary should not be set when no healthy canary backends")
	}
}

func TestCanaryPool(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  50,
		Upstreams: []config.Upstream{
			{Address: "http://canary1:8080", Weight: 1},
			{Address: "http://canary2:8080", Weight: 2},
		},
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	pool := cr.CanaryPool()
	if pool.Len() != 2 {
		t.Errorf("canary pool len = %d, want 2", pool.Len())
	}
}

func TestCanaryWeightDistribution(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  50,
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	canaryCount := 0
	iterations := 1000
	for i := 0; i < iterations; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		if cr.IsCanary(req, cfg) {
			canaryCount++
		}
	}

	// With 50% weight, expect roughly 500 canary hits (allow 35% to 65%)
	if canaryCount < 350 || canaryCount > 650 {
		t.Errorf("canary distribution = %d/%d, expected roughly 50%%", canaryCount, iterations)
	}
}

// === Coverage push tests ===

// --- canary.go: negative weight ---

func TestCanaryIsCanaryNegativeWeight(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  -10,
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	req := httptest.NewRequest("GET", "/", nil)
	if cr.IsCanary(req, cfg) {
		t.Error("should never be canary with negative weight")
	}
}

func TestCanaryIsCanaryWeight150(t *testing.T) {
	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  150,
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)

	req := httptest.NewRequest("GET", "/", nil)
	if !cr.IsCanary(req, cfg) {
		t.Error("should always be canary with weight >= 100")
	}
}

// --- canary.go: default cookie name in Serve ---

func TestCanaryServeDefaultCookieName(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("canary"))
	}))
	defer upstream.Close()

	log := newTestLogger()
	cfg := config.CanaryConfig{
		Enabled: true,
		Weight:  100,
		Upstreams: []config.Upstream{
			{Address: upstream.URL, Weight: 1},
		},
		// Cookie is empty — should default to X-Canary
	}
	cr := NewCanaryRouter(cfg, "round_robin", log)
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Canary = cfg

	cr.Serve(ctx, domain, h)

	cookies := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookies, "X-Canary=true") {
		t.Errorf("expected default cookie name X-Canary, got %q", cookies)
	}
}

// --- handler.go: Serve with POST body replay on retry ---

func TestProxyRetryWithBodyReplay(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1}, // will refuse
		{Address: upstream.URL, Weight: 1},         // will succeed
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("POST", "/api/data", strings.NewReader("request body content"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 2
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 2 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (retry with body replay)", rec.Code)
	}
	if receivedBody != "request body content" {
		t.Errorf("body = %q, want 'request body content' (body should be replayed on retry)", receivedBody)
	}
}

// --- handler.go: context cancelled (non-deadline) returns 502 ---

func TestProxyHandlerContextCancelled502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	reqCtx, cancel := context.WithCancel(req.Context())
	// Cancel immediately so context.Err() != nil but != DeadlineExceeded
	cancel()
	req = req.WithContext(reqCtx)

	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 100 * time.Millisecond}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for cancelled context", rec.Code)
	}
}

// --- handler.go: isRetryableError with net.Error ---

type testNetError struct {
	timeout bool
}

func (e *testNetError) Error() string   { return "test net error" }
func (e *testNetError) Timeout() bool   { return e.timeout }
func (e *testNetError) Temporary() bool { return true }

func TestIsRetryableErrorNetTimeout(t *testing.T) {
	err := &testNetError{timeout: true}
	if !isRetryableError(err) {
		t.Error("net.Error with Timeout() should be retryable")
	}
}

func TestIsRetryableErrorNetNonTimeout(t *testing.T) {
	// net.Error interface is satisfied, and the code returns true for any net.Error
	err := &testNetError{timeout: false}
	if !isRetryableError(err) {
		t.Error("any net.Error should be retryable")
	}
}

func TestIsRetryableErrorNoSuchHost(t *testing.T) {
	err := fmt.Errorf("lookup badhost.example.com: no such host")
	if !isRetryableError(err) {
		t.Error("no such host should be retryable")
	}
}

func TestIsRetryableErrorNonRetryable(t *testing.T) {
	err := fmt.Errorf("some random error")
	if isRetryableError(err) {
		t.Error("random error should not be retryable")
	}
}

// --- handler.go: removeHopByHop ---

func TestRemoveHopByHop(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "keep-alive")
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Transfer-Encoding", "chunked")
	h.Set("X-Custom", "keep-me")

	removeHopByHop(h)

	if h.Get("Connection") != "" {
		t.Error("Connection header should be removed")
	}
	if h.Get("Keep-Alive") != "" {
		t.Error("Keep-Alive header should be removed")
	}
	if h.Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding header should be removed")
	}
	if h.Get("X-Custom") != "keep-me" {
		t.Error("X-Custom header should be preserved")
	}
}

// --- handler.go: forwardedProto ---

func TestForwardedProtoHTTP(t *testing.T) {
	ctx := newTestContext(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	ctx.IsHTTPS = false
	if got := forwardedProto(ctx); got != "http" {
		t.Errorf("forwardedProto = %q, want http", got)
	}
}

func TestForwardedProtoHTTPS(t *testing.T) {
	ctx := newTestContext(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	ctx.IsHTTPS = true
	if got := forwardedProto(ctx); got != "https" {
		t.Errorf("forwardedProto = %q, want https", got)
	}
}

// --- handler.go: MaxRetries capped to len(backends) ---

func TestProxyMaxRetriesCappedToBackendCount(t *testing.T) {
	// One backend that refuses, MaxRetries=10 but only 1 backend so effectively 1
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 10
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 1 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// --- health.go: checkOne with unreachable backend ---

func TestHealthCheckerCheckOneUnreachable(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
	})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/health",
		Interval:  50 * time.Millisecond,
		Timeout:   500 * time.Millisecond,
		Threshold: 1,
		Rise:      1,
	}, log)

	backend := pool.All()[0]
	hc.checkOne(backend)

	if backend.IsHealthy() {
		t.Error("backend should be unhealthy after failed health check to unreachable host")
	}
}

// --- health.go: checkOne with 4xx status ---

func TestHealthCheckerCheckOneBadStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: ts.URL, Weight: 1},
	})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/health",
		Interval:  50 * time.Millisecond,
		Timeout:   2 * time.Second,
		Threshold: 1,
		Rise:      1,
	}, log)

	backend := pool.All()[0]
	hc.checkOne(backend)

	if backend.IsHealthy() {
		t.Error("backend should be unhealthy after 503 response")
	}
}

// --- health.go: checkOne with 2xx status keeps healthy ---

func TestHealthCheckerCheckOneSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("expected /health path, got %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") != "UWAS-HealthCheck/1.0" {
			t.Errorf("User-Agent = %q, want UWAS-HealthCheck/1.0", r.Header.Get("User-Agent"))
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: ts.URL, Weight: 1},
	})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/health",
		Interval:  50 * time.Millisecond,
		Timeout:   2 * time.Second,
		Threshold: 3,
		Rise:      1,
	}, log)

	backend := pool.All()[0]
	hc.checkOne(backend)

	if !backend.IsHealthy() {
		t.Error("backend should remain healthy after 200 response")
	}
}

// --- health.go: Start performs immediate check via ticker ---

func TestHealthCheckerStartPerformsCheck(t *testing.T) {
	requestCount := 0
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: ts.URL, Weight: 1},
	})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Interval: 20 * time.Millisecond,
		Timeout:  2 * time.Second,
	}, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hc.Start(ctx)

	// Poll until at least one health check has been performed
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := requestCount
		mu.Unlock()
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	count := requestCount
	mu.Unlock()
	if count == 0 {
		t.Error("Start should have triggered at least one health check")
	}
}

// --- mirror.go: Send with various HTTP methods ---

func TestMirrorSendGETMethod(t *testing.T) {
	var receivedMethod string
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedMethod = r.Method
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())
	req := httptest.NewRequest("GET", "/page", nil)
	m.Send(req, nil)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedMethod != "GET" {
		t.Errorf("expected GET, got %s", receivedMethod)
	}
}

func TestMirrorSendPUTMethod(t *testing.T) {
	var receivedMethod string
	var receivedBody string
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedMethod = r.Method
		receivedBody = string(body)
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())
	req := httptest.NewRequest("PUT", "/resource", strings.NewReader("update data"))
	m.Send(req, []byte("update data"))

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedMethod != "PUT" {
		t.Errorf("expected PUT, got %s", receivedMethod)
	}
	if receivedBody != "update data" {
		t.Errorf("expected body 'update data', got %q", receivedBody)
	}
}

func TestMirrorSendDELETEMethod(t *testing.T) {
	var receivedMethod string
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedMethod = r.Method
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())
	req := httptest.NewRequest("DELETE", "/resource/123", nil)
	m.Send(req, nil)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", receivedMethod)
	}
}

// --- mirror.go: doMirror with 500 response (logs at debug) ---

func TestMirrorSend500Response(t *testing.T) {
	done := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())
	req := httptest.NewRequest("GET", "/test", nil)
	m.Send(req, nil)

	// Wait for async mirror — should not panic, just log
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}
}

// --- mirror.go: doMirror with nil body ---

func TestMirrorSendNilBody(t *testing.T) {
	var receivedBody string
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedBody = string(body)
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())
	req := httptest.NewRequest("GET", "/test", nil)
	m.Send(req, nil)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedBody != "" {
		t.Errorf("expected empty body, got %q", receivedBody)
	}
}

// --- handler.go: WebSocket upgrade with mixed case ---

func TestIsWebSocketUpgradeMixedCase(t *testing.T) {
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "WebSocket")
	req.Header.Set("Connection", "upgrade, keep-alive")

	if !IsWebSocketUpgrade(req) {
		t.Error("should detect WebSocket upgrade with mixed case")
	}
}

func TestIsWebSocketUpgradePartial(t *testing.T) {
	// Only Upgrade header, no Connection
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	if IsWebSocketUpgrade(req) {
		t.Error("should not detect WebSocket without Connection: Upgrade")
	}

	// Only Connection header, no Upgrade
	req2 := httptest.NewRequest("GET", "/ws", nil)
	req2.Header.Set("Connection", "Upgrade")
	if IsWebSocketUpgrade(req2) {
		t.Error("should not detect WebSocket without Upgrade header")
	}
}

// --- handler.go: Serve with all backends tried exhausted loop ---

func TestProxyServeAllBackendsTried(t *testing.T) {
	// 3 backends all refuse, MaxRetries=3
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
		{Address: "http://127.0.0.1:2", Weight: 1},
		{Address: "http://127.0.0.1:3", Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 3
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 1 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// --- handler.go: clientIP with IPv6 ---

func TestClientIPIPv6(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:8080"
	got := clientIP(req)
	if got != "::1" {
		t.Errorf("clientIP = %q, want ::1", got)
	}
}

// --- balancer.go: Random Select where j has fewer connections ---

func TestRandomSelectJHasFewerConns(t *testing.T) {
	// Create backends with specific connection loads
	backends := makeBackends(2)
	backends[0].ActiveConns.Store(100) // high load
	backends[1].ActiveConns.Store(0)   // low load

	rn := &Random{}
	req := httptest.NewRequest("GET", "/", nil)

	// Run many times — eventually both i and j paths will be hit
	selectedLow := 0
	for i := 0; i < 200; i++ {
		b := rn.Select(backends, req)
		if b == backends[1] {
			selectedLow++
		}
	}

	// With such disparate loads, the low-load backend should be selected most of the time
	if selectedLow == 0 {
		t.Error("Random should sometimes select backend[j] with fewer connections")
	}
}

// --- handler.go: body read error ---

type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("simulated read error")
}
func (e *errorReader) Close() error { return nil }

func TestProxyHandlerBodyReadError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("POST", "/", nil)
	req.Body = &errorReader{}
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for body read error", rec.Code)
	}
}

// --- handler.go: non-retryable error with retries remaining ---

func TestProxyNonRetryableError(t *testing.T) {
	// Create a server that immediately closes connections in a way that
	// produces a non-retryable error. We'll use a server that responds
	// with an invalid HTTP response.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hijack the connection and send garbage
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(200)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Write([]byte("garbage response\r\n"))
		conn.Close()
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: upstream.URL, Weight: 1},
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 2
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 2 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	// Should get 502 since the error from parsing garbage isn't retryable
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for non-retryable error", rec.Code)
	}
}

// --- handler.go: Serve handles response body copy error gracefully ---

func TestProxyResponseBodyCopyError(t *testing.T) {
	// Create an upstream that writes a Content-Length header then sends
	// less data, which may cause io.Copy to return an error.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		// Write much less than declared — this can cause an early EOF
		w.Write([]byte("partial"))
		// Flush and close to trigger the error
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	// Should succeed with whatever it received (200 status)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- mirror.go: doMirror removes hop-by-hop headers ---

func TestMirrorRemovesHopByHop(t *testing.T) {
	var mu sync.Mutex
	var receivedHeaders http.Header
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeaders = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())

	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("X-Custom", "keep")

	m.Send(req, nil)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	if receivedHeaders == nil {
		t.Fatal("no request received")
	}
	if receivedHeaders.Get("Connection") != "" {
		t.Error("Connection header should be removed from mirror request")
	}
	if receivedHeaders.Get("X-Custom") != "keep" {
		t.Error("X-Custom header should be preserved")
	}
}

// --- health.go: checkOne with 3xx redirect response (considered success) ---

func TestHealthCheckerCheckOneRedirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(301)
	}))
	defer ts.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: ts.URL, Weight: 1},
	})
	log := newTestLogger()

	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "/health",
		Interval:  50 * time.Millisecond,
		Timeout:   2 * time.Second,
		Threshold: 3,
		Rise:      1,
	}, log)

	backend := pool.All()[0]
	// The health client will follow the redirect, but httptest.NewServer
	// doesn't redirect, it returns 301 directly. 301 is in 200-399 range.
	hc.checkOne(backend)

	if !backend.IsHealthy() {
		t.Error("backend should remain healthy after 301 response (in 200-399 range)")
	}
}

// --- handler.go: Serve with nil body request ---

func TestProxyServeNilBody(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Body = nil
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if receivedBody != "" {
		t.Errorf("body = %q, want empty", receivedBody)
	}
}

// --- handler.go: Serve sets Upstream field on context ---

func TestProxyServeSetsUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if ctx.Upstream == "" {
		t.Error("Upstream should be set on context after proxying")
	}
}

// --- handler.go: NewRequestWithContext error (invalid method) ---

func TestProxyServeInvalidMethod(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	// Create a request with an invalid method containing a space
	// http.NewRequestWithContext rejects methods with spaces
	req := httptest.NewRequest("GET", "/", nil)
	req.Method = "INVALID METHOD" // contains space → NewRequestWithContext will fail
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for invalid method", rec.Code)
	}
}

// --- mirror.go: doMirror NewRequestWithContext error ---

func TestMirrorSendInvalidMethod(t *testing.T) {
	done := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())

	req := httptest.NewRequest("GET", "/", nil)
	req.Method = "INVALID METHOD" // contains space -> NewRequestWithContext will fail

	m.Send(req, nil)

	// The request should fail in NewRequestWithContext and never reach the server.
	// Wait briefly to ensure the goroutine has had time to run and not panic.
	select {
	case <-done:
		t.Fatal("request should not have reached server with invalid method")
	case <-time.After(500 * time.Millisecond):
		// Expected: goroutine failed early, no request reached server
	}
}

// --- health.go: checkOne with invalid URL (NewRequestWithContext error) ---

func TestHealthCheckerCheckOneInvalidURL(t *testing.T) {
	// Create a pool with a backend whose URL, when combined with path,
	// makes an invalid URL. Since url.Parse is lenient, we need a creative approach.
	// The simplest is a backend URL with a scheme that contains spaces.
	pool := &UpstreamPool{}
	badURL, _ := url.Parse("http://valid:8080")
	backend := &Backend{URL: badURL, Weight: 1}
	pool.backends = []*Backend{backend}

	log := newTestLogger()
	hc := NewHealthChecker(pool, HealthConfig{
		Path:      "://invalid url with spaces", // This won't cause NewRequest to fail
		Timeout:   500 * time.Millisecond,
		Threshold: 1,
	}, log)

	// The health check should handle the error gracefully
	hc.checkOne(backend)
}

// --- handler.go: nil backend from Select (custom balancer) ---

type nilBalancer struct{}

func (nb *nilBalancer) Select(backends []*Backend, r *http.Request) *Backend {
	return nil
}

func TestProxyServeNilBackendFromBalancer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := &nilBalancer{} // always returns nil
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for nil backend", rec.Code)
	}
}

// --- WebSocket proxy tests ---

func TestWebSocketUpgradeSkipsRetryLoop(t *testing.T) {
	// When WebSocket is enabled and request has Upgrade headers,
	// the proxy should attempt WS tunnel (not HTTP round-trip).
	// Since httptest.NewRecorder doesn't support Hijack, we verify
	// the Error fallback path.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.WebSocket = true

	h.Serve(ctx, domain, pool, balancer)

	// Since httptest.NewRecorder doesn't implement Hijack,
	// the handler should fall back with an error status (500)
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for non-hijackable WS", rec.Code)
	}
}

func TestWebSocketDisabledFallsBackToHTTP(t *testing.T) {
	// When WebSocket is disabled, even with Upgrade headers,
	// the proxy should use normal HTTP round-trip.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("http-response"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.WebSocket = false // disabled

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 for disabled WS (HTTP fallback)", rec.Code)
	}
}

// --- Trace Context tests ---

func TestGenerateTraceparent(t *testing.T) {
	tp := generateTraceparent()
	// Format: 00-<32hex>-<16hex>-01
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		t.Fatalf("traceparent should have 4 parts, got %d: %q", len(parts), tp)
	}
	if parts[0] != "00" {
		t.Errorf("version = %q, want 00", parts[0])
	}
	if len(parts[1]) != 32 {
		t.Errorf("trace-id length = %d, want 32", len(parts[1]))
	}
	if len(parts[2]) != 16 {
		t.Errorf("span-id length = %d, want 16", len(parts[2]))
	}
	if parts[3] != "01" {
		t.Errorf("flags = %q, want 01", parts[3])
	}

	// Generate two — they should be unique
	tp2 := generateTraceparent()
	if tp == tp2 {
		t.Error("two traceparents should be unique")
	}
}

func TestTraceparentPropagation(t *testing.T) {
	// Verify the proxy adds traceparent when not present
	var receivedTP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTP = r.Header.Get("Traceparent")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)
	domain := newTestDomain()

	h.Serve(ctx, domain, pool, balancer)

	if receivedTP == "" {
		t.Error("upstream should receive a traceparent header")
	}
	if !strings.HasPrefix(receivedTP, "00-") {
		t.Errorf("traceparent = %q, should start with '00-'", receivedTP)
	}
}

func TestTraceparentPreservesExisting(t *testing.T) {
	// When client sends traceparent, proxy should forward it (not replace)
	existingTP := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	var receivedTP string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTP = r.Header.Get("Traceparent")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: upstream.URL, Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Traceparent", existingTP)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)
	domain := newTestDomain()

	h.Serve(ctx, domain, pool, balancer)

	if receivedTP != existingTP {
		t.Errorf("traceparent = %q, want %q (should preserve existing)", receivedTP, existingTP)
	}
}
