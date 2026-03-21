package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
