package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

// --- balancer.go: StickyBalancer.Select (cookie hit, fallback, gone backend) ---

func TestStickyBalancerSelect(t *testing.T) {
	backends := makeBackends(3)
	sb := &StickyBalancer{CookieName: "uwas_sticky", TTL: 3600}

	// Empty backends → nil
	req := httptest.NewRequest("GET", "/", nil)
	if sb.Select(nil, req) != nil {
		t.Error("StickyBalancer.Select(nil) should return nil")
	}

	// No cookie → falls back to round-robin (non-nil)
	if got := sb.Select(backends, req); got == nil {
		t.Error("StickyBalancer.Select without cookie should fall back to round-robin")
	}

	// Cookie matching an existing backend host → that backend
	target := backends[1]
	reqWithCookie := httptest.NewRequest("GET", "/", nil)
	reqWithCookie.AddCookie(&http.Cookie{Name: "uwas_sticky", Value: target.URL.Host})
	if got := sb.Select(backends, reqWithCookie); got != target {
		t.Errorf("StickyBalancer.Select with cookie = %v, want %v", got.URL.Host, target.URL.Host)
	}

	// Cookie value for a backend that no longer exists → fall back
	reqGone := httptest.NewRequest("GET", "/", nil)
	reqGone.AddCookie(&http.Cookie{Name: "uwas_sticky", Value: "ghost:9999"})
	if got := sb.Select(backends, reqGone); got == nil {
		t.Error("StickyBalancer.Select with stale cookie should fall back to round-robin")
	}

	// Empty cookie value → fall back
	reqEmpty := httptest.NewRequest("GET", "/", nil)
	reqEmpty.AddCookie(&http.Cookie{Name: "uwas_sticky", Value: ""})
	if got := sb.Select(backends, reqEmpty); got == nil {
		t.Error("StickyBalancer.Select with empty cookie should fall back")
	}
}

// --- balancer.go: SetStickyCookie ---

func TestSetStickyCookie(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	ctx := newTestContext(httptest.NewRecorder(), req)

	SetStickyCookie(ctx.Response, "uwas_sticky", "backend-1:8080", 1800)

	setCookie := ctx.Response.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "uwas_sticky=backend-1:8080") {
		t.Errorf("Set-Cookie = %q, want sticky cookie", setCookie)
	}
	if !strings.Contains(setCookie, "Max-Age=1800") {
		t.Errorf("Set-Cookie = %q, want Max-Age=1800", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") {
		t.Errorf("Set-Cookie = %q, want HttpOnly", setCookie)
	}
}

// --- balancer.go: NewBalancer sticky + uri_hash + dashed forms ---

func TestNewBalancerSticky(t *testing.T) {
	b := NewBalancer("sticky")
	sb, ok := b.(*StickyBalancer)
	if !ok {
		t.Fatalf("NewBalancer(sticky) = %T, want *StickyBalancer", b)
	}
	if sb.CookieName != "uwas_sticky" || sb.TTL != 3600 {
		t.Errorf("sticky defaults wrong: cookie=%q ttl=%d", sb.CookieName, sb.TTL)
	}
}

func TestNewBalancerURIHashAndDashedForms(t *testing.T) {
	if _, ok := NewBalancer("uri_hash").(*URIHash); !ok {
		t.Error("uri_hash should map to URIHash")
	}
	// Dashed form should normalize to the same balancer.
	if _, ok := NewBalancer("least-conn").(*LeastConn); !ok {
		t.Error("least-conn (dashed) should map to LeastConn")
	}
	// "weighted" maps to RoundRobin (default branch).
	if _, ok := NewBalancer("weighted").(*RoundRobin); !ok {
		t.Error("weighted should map to RoundRobin")
	}
}

// --- circuit.go: allowHalfOpenProbe returns false when slot already claimed ---

func TestAllowHalfOpenProbeSlotClaimed(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}
	time.Sleep(60 * time.Millisecond)

	// First Allow transitions Open→HalfOpen and claims the probe slot.
	if !cb.Allow() {
		t.Fatal("first probe should be allowed")
	}
	// Now in HalfOpen with slot claimed: a direct allowHalfOpenProbe must fail.
	if cb.allowHalfOpenProbe() {
		t.Error("allowHalfOpenProbe should return false when slot already claimed")
	}
}

// allowHalfOpenProbe true-branch: a fresh breaker has probeSlot==0, so the
// CAS succeeds and returns true.
func TestAllowHalfOpenProbeSlotFree(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Second)
	if !cb.allowHalfOpenProbe() {
		t.Error("allowHalfOpenProbe should return true when slot is free")
	}
	// Second direct call: slot now taken → false.
	if cb.allowHalfOpenProbe() {
		t.Error("second allowHalfOpenProbe should return false")
	}
}

// Allow() entering an already-HalfOpen state routes through allowHalfOpenProbe.
// Reset the breaker into HalfOpen with a free slot to drive that path.
func TestAllowInHalfOpenState(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Second)
	// Force half-open with a free probe slot.
	cb.state.Store(int32(CircuitHalfOpen))
	cb.probeSlot.Store(0)

	if !cb.Allow() {
		t.Error("Allow in half-open with free slot should be allowed")
	}
	if cb.Allow() {
		t.Error("Allow in half-open with claimed slot should be rejected")
	}
}

// --- circuit.go: Allow in Open state, second goroutine path via half-open ---
// After the breaker transitions to half-open and the probe slot is claimed,
// a subsequent Allow() that re-enters the Open branch (timeout still elapsed)
// finds the state already half-open and routes through allowHalfOpenProbe,
// which rejects because the slot is taken.
func TestCircuitBreakerOpenThenHalfOpenContended(t *testing.T) {
	cb := NewCircuitBreaker(1, 30*time.Millisecond)
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}
	time.Sleep(40 * time.Millisecond)

	// Claim the probe via the half-open transition.
	if !cb.Allow() {
		t.Fatal("first probe should be allowed")
	}
	// Subsequent calls in half-open are rejected (slot taken).
	for i := 0; i < 3; i++ {
		if cb.Allow() {
			t.Error("contended half-open probe should be rejected")
		}
	}
}

// circuit.go: contended Open→HalfOpen transition. Many goroutines call Allow()
// the instant the open timeout elapses; exactly one wins the CAS transition and
// claims the probe, the losers fall through the "another goroutine won" path
// (lines 62-70). Run under -race to shake out the interleavings.
func TestCircuitBreakerContendedTransition(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		cb := NewCircuitBreaker(1, 5*time.Millisecond)
		cb.RecordFailure() // → open
		time.Sleep(6 * time.Millisecond)

		var wg sync.WaitGroup
		allowed := make([]bool, 32)
		start := make(chan struct{})
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				allowed[idx] = cb.Allow()
			}(i)
		}
		close(start)
		wg.Wait()

		// At most one probe should have been admitted.
		count := 0
		for _, a := range allowed {
			if a {
				count++
			}
		}
		if count > 1 {
			t.Fatalf("iter %d: %d probes admitted concurrently, want <=1", iter, count)
		}
	}
}

// --- handler.go: getTransport with all timeout overrides + InsecureSkipVerify ---

func TestGetTransportTimeoutOverrides(t *testing.T) {
	log := newTestLogger()
	h := New(log)

	domain := newTestDomain()
	domain.Proxy.Timeouts.Connect = config.Duration{Duration: 3 * time.Second}
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 7 * time.Second}
	domain.Proxy.Timeouts.Write = config.Duration{Duration: 11 * time.Second}
	domain.Proxy.InsecureSkipVerify = true

	tr := h.getTransport(domain)
	if tr == nil {
		t.Fatal("getTransport returned nil")
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 7s", tr.ResponseHeaderTimeout)
	}
	if tr.ExpectContinueTimeout != 11*time.Second {
		t.Errorf("ExpectContinueTimeout = %v, want 11s", tr.ExpectContinueTimeout)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be set on transport TLS config")
	}

	// Cached on second call (same pointer).
	if h.getTransport(domain) != tr {
		t.Error("getTransport should cache the transport per domain")
	}
}

// --- handler.go: WebSocket upgrade but no backend selected → 502 ---
// A balancer that always returns nil exercises the "no backend selected" path.
// Reuses the existing package-level nilBalancer type.

func TestServeWebSocketNoBackendSelected(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://127.0.0.1:9", Weight: 1}})
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.WebSocket = true

	h.Serve(ctx, domain, pool, &nilBalancer{})

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when WS balancer selects no backend", rec.Code)
	}
}

// --- handler.go: 413 when buffered request body exceeds maxRetryBodyBytes ---

func TestServeRequestEntityTooLarge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	// Body larger than maxRetryBodyBytes (8MB) but with a ContentLength small
	// enough to enter the buffering branch — set ContentLength to the cap so
	// the branch is taken, then provide an oversized body.
	big := strings.NewReader(strings.Repeat("a", int(maxRetryBodyBytes)+10))
	req := httptest.NewRequest("POST", "/upload", big)
	req.ContentLength = maxRetryBodyBytes // within the buffering window
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 1

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for oversized buffered body", rec.Code)
	}
}

// --- handler.go: large/unknown body disables retries (ContentLength < 0) ---

func TestServeUnknownLengthBodyDisablesRetry(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("POST", "/stream", strings.NewReader("streamed body"))
	req.ContentLength = -1 // unknown length → maxRetries forced to 0
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.MaxRetries = 3

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- handler.go: SSRF block with AllowPrivateUpstreams (metadata still blocked) ---

func TestServeSSRFBlockedMetadataEvenWhenPrivateAllowed(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://169.254.169.254:80", Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/latest/meta-data/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.AllowPrivateUpstreams = true // uses IsPrivateProxyUpstreamSafe path

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (metadata blocked even with private upstreams allowed)", rec.Code)
	}
}

func TestServeSSRFBlockedDefault(t *testing.T) {
	// Private upstream blocked by default (IsProxyUpstreamSafe path).
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://169.254.169.254:80", Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	h.Serve(ctx, newTestDomain(), pool, balancer)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (SSRF default block)", rec.Code)
	}
}

// --- handler.go: buffered response mode (BufferResponse=true) ---

func TestServeBufferedResponse(t *testing.T) {
	body := "buffered upstream body"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "22")
		w.WriteHeader(200)
		w.Write([]byte(body))
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
	domain.Proxy.BufferResponse = true

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != body {
		t.Errorf("buffered body = %q, want %q", rec.Body.String(), body)
	}
}

// --- handler.go:307-312: buffered response read error (truncated upstream) ---
// A raw TCP upstream advertises a Content-Length larger than the body it sends,
// then closes the connection. In buffered mode io.ReadAll hits an unexpected
// EOF, exercising the read-error branch.
func TestServeBufferedResponseReadError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				_, _ = c.Read(buf) // consume request
				// Claim 100 bytes but send only 5, then close → truncated body.
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nhello"))
				c.Close()
			}(conn)
		}
	}()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://" + ln.Addr().String(), Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	domain := newTestDomain()
	domain.Proxy.BufferResponse = true
	domain.Proxy.MaxRetries = 0
	domain.Proxy.Timeouts.Read = config.Duration{Duration: 2 * time.Second}

	h.Serve(ctx, domain, pool, balancer)

	// Headers (200) were already written before the read error; the partial
	// body is intentionally not flushed. We only assert it does not panic and
	// status is the upstream's 200.
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (headers sent before read error)", rec.Code)
	}
}

// --- handler.go: sticky balancer sets cookie on the proxied response ---

func TestServeWithStickyBalancerSetsCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("sticky")
	log := newTestLogger()
	h := New(log)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := newTestContext(rec, req)

	h.Serve(ctx, newTestDomain(), pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if sc := rec.Header().Get("Set-Cookie"); !strings.Contains(sc, "uwas_sticky=") {
		t.Errorf("Set-Cookie = %q, want sticky cookie from StickyBalancer", sc)
	}
}

// --- mirror.go: MaxBodyBytes accessor (default + custom) ---

func TestMirrorMaxBodyBytes(t *testing.T) {
	def := NewMirror(MirrorConfig{Enabled: true, Backend: "http://x", Percent: 100}, testLogger())
	if def.MaxBodyBytes() != 2<<20 {
		t.Errorf("default MaxBodyBytes = %d, want %d", def.MaxBodyBytes(), 2<<20)
	}

	custom := NewMirror(MirrorConfig{Enabled: true, Backend: "http://x", Percent: 100, MaxBodyBytes: 4096}, testLogger())
	if custom.MaxBodyBytes() != 4096 {
		t.Errorf("custom MaxBodyBytes = %d, want 4096", custom.MaxBodyBytes())
	}
}

// --- health.go: Stop resets internal maps ---

func TestHealthCheckerStop(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://127.0.0.1:1", Weight: 1}})
	log := newTestLogger()
	hc := NewHealthChecker(pool, HealthConfig{Threshold: 1, Rise: 1}, log)

	b := pool.All()[0]
	// Seed the failure map.
	hc.recordFailure(b)
	hc.mu.Lock()
	seeded := hc.failures[b]
	hc.mu.Unlock()
	if seeded == 0 {
		t.Fatal("failure should have been recorded")
	}

	hc.Stop()

	hc.mu.Lock()
	defer hc.mu.Unlock()
	if len(hc.failures) != 0 || len(hc.successes) != 0 {
		t.Error("Stop should reset failure/success maps")
	}
}

// --- handler.go: serveWebSocket upstream connect failure writes 502 to client ---
// Uses a hijackable ResponseWriter via a real net listener through httptest.

func TestServeWebSocketUpstreamUnreachable(t *testing.T) {
	// Backend that does not exist → DialTimeout fails → 502 written to client.
	u, _ := url.Parse("http://127.0.0.1:1")
	backend := &Backend{URL: u, Weight: 1}

	log := newTestLogger()
	h := New(log)

	// Drive through a real HTTP server so the ResponseWriter supports Hijack.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := router.AcquireContext(w, r)
		h.serveWebSocket(ctx, backend)
	}))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for unreachable WS upstream", resp.StatusCode)
	}
}
