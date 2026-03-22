package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestHandlerName(t *testing.T) {
	log := newTestLogger()
	h := New(log)
	if got := h.Name(); got != "proxy" {
		t.Errorf("Name() = %q, want %q", got, "proxy")
	}
}

func TestHandlerDescription(t *testing.T) {
	log := newTestLogger()
	h := New(log)
	if got := h.Description(); got != "Reverse proxy with load balancing" {
		t.Errorf("Description() = %q, want %q", got, "Reverse proxy with load balancing")
	}
}

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
	failAfter := 0   // start healthy
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
		time.Sleep(500 * time.Millisecond)
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
	reqCtx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(reqCtx)

	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 50 * time.Millisecond}

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

// --- circuit.go: HalfOpen max requests exceeded ---

func TestCircuitBreakerHalfOpenMaxExceeded(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Trip the breaker
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open after 2 failures")
	}

	// Wait for timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)

	// First Allow() transitions from Open to HalfOpen (resets halfOpenCount=0, returns true)
	if !cb.Allow() {
		t.Fatal("first call should be allowed (transitions to half-open)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatal("should be half-open now")
	}

	// Second Allow() enters HalfOpen path: halfOpenCount(0) < halfOpenMax(1) → increments to 1, returns true
	if !cb.Allow() {
		t.Fatal("second call should be allowed (halfOpenCount < halfOpenMax)")
	}

	// Third Allow(): halfOpenCount(1) < halfOpenMax(1) is false → rejected
	if cb.Allow() {
		t.Error("third call should be rejected in half-open (max exceeded)")
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
