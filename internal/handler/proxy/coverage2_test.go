package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// TestCircuitBreakerHalfOpenRejectExcess covers the HalfOpen rejection path
// where a second concurrent probe is rejected via CAS on probeSlot.
func TestCircuitBreakerHalfOpenRejectExcess(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Trip to open
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatal("should be open")
	}

	// Wait for timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)

	// First Allow() transitions to half-open and claims the probe slot
	if !cb.Allow() {
		t.Error("first call in half-open should be allowed")
	}

	// Second Allow() in half-open should be rejected because probeSlot is already taken
	if cb.Allow() {
		t.Error("second call in half-open should be rejected (probeSlot already claimed)")
	}
}

// TestCircuitBreakerDefaultValues covers NewCircuitBreaker with zero/negative
// values to exercise the default assignment paths.
func TestCircuitBreakerDefaultValues(t *testing.T) {
	cb := NewCircuitBreaker(0, 0)
	if cb.threshold != 5 {
		t.Errorf("threshold = %d, want 5 (default)", cb.threshold)
	}
	if cb.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s (default)", cb.timeout)
	}

	cb2 := NewCircuitBreaker(-1, -1*time.Second)
	if cb2.threshold != 5 {
		t.Errorf("threshold = %d, want 5 (default)", cb2.threshold)
	}
}

// TestServeWebSocketHTTPDefaultPort covers the HTTP default port (80) branch
// in serveWebSocket (line 306) when URL host has no port and scheme is http.
func TestServeWebSocketHTTPDefaultPort(t *testing.T) {
	u, _ := url.Parse("http://example.com")
	backend := &Backend{URL: u, Weight: 1}

	log := logger.New("error", "text")
	h := New(log)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		buf := make([]byte, 4096)
		clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		clientConn.Read(buf)
		clientConn.Close()
	}()

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "1.2.3.4:5678"

	hjWriter := &covHijackableWriter{conn: serverConn}
	ctx := router.AcquireContext(hjWriter, req)

	// Will fail to connect to example.com:80 but exercises the default port code.
	h.serveWebSocket(ctx, backend)
}

// TestServeWebSocketWSSDefaultPort covers the WSS/HTTPS default port (:443).
func TestServeWebSocketWSSDefaultPort(t *testing.T) {
	u, _ := url.Parse("wss://example.com")
	backend := &Backend{URL: u, Weight: 1}

	log := logger.New("error", "text")
	h := New(log)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		buf := make([]byte, 4096)
		clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		clientConn.Read(buf)
		clientConn.Close()
	}()

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "1.2.3.4:5678"

	hjWriter := &covHijackableWriter{conn: serverConn}
	ctx := router.AcquireContext(hjWriter, req)

	h.serveWebSocket(ctx, backend)
}

// TestServeNoHealthyUpstreams covers Serve when all backends are unhealthy.
func TestServeNoHealthyUpstreams(t *testing.T) {
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://localhost:3000", Weight: 1}})
	pool.backends[0].SetState(StateUnhealthy)

	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// TestServeWebSocketUpgradePath covers the WebSocket upgrade detection in Serve.
func TestServeWebSocketUpgradePath(t *testing.T) {
	// Create a backend listener that immediately closes (to test WS code path)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	backendAddr := listener.Addr().String()
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://" + backendAddr, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	// Build a WebSocket request
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		buf := make([]byte, 4096)
		clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientConn.Read(buf)
		clientConn.Close()
	}()

	req := httptest.NewRequest("GET", "/ws-path", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "1.2.3.4:5678"

	hjWriter := &covHijackableWriter{conn: serverConn}
	ctx := router.AcquireContext(hjWriter, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			WebSocket: true,
		},
	}

	h.Serve(ctx, domain, pool, balancer)
}

// TestServeContextDeadlineExceeded covers the client context deadline path
// (lines 181-184 in handler.go).
func TestServeContextDeadlineExceeded(t *testing.T) {
	// Create a backend that takes too long to respond
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	// Create a request with a very short context deadline
	ctx2, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest("GET", "/slow", nil)
	req = req.WithContext(ctx2)

	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Timeouts: config.ProxyTimeouts{Read: config.Duration{Duration: 10 * time.Second}},
		},
	}

	h.Serve(ctx, domain, pool, balancer)

	// Should get 504 Gateway Timeout since the client context expires first
	if rec.Code != 504 && rec.Code != 502 {
		t.Errorf("status = %d, want 504 or 502", rec.Code)
	}
}

// TestServeRetryExhausted covers the "all backends tried" break path
// (line 99-101) and the "all retries exhausted" path (line 223).
func TestServeRetryExhausted(t *testing.T) {
	// Create two backends that both refuse connections
	pool := NewUpstreamPool([]UpstreamConfig{
		{Address: "http://127.0.0.1:1", Weight: 1},
		{Address: "http://127.0.0.1:2", Weight: 1},
	})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/retry-test", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			MaxRetries: 3,
			Timeouts:   config.ProxyTimeouts{Read: config.Duration{Duration: 1 * time.Second}},
		},
	}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// TestServeResponseCopyError covers the io.Copy error in the response body
// (lines 212-217). We simulate this with a backend that sends a body and then
// resets the connection.
func TestServeResponseCopyError(t *testing.T) {
	// Create a backend that sends headers, starts body, then closes abruptly
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		conn.Read(buf)

		// Send partial response then close
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 1000000\r\n\r\npartial"))
		time.Sleep(10 * time.Millisecond)
		conn.Close()
	}()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://" + listener.Addr().String(), Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/body-error", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)
	// Just verify it doesn't panic - the error is logged internally
}

// TestServeWithExistingTraceparent covers the case where the request already
// has a Traceparent header (line 162-164 - the if block is skipped).
func TestServeWithExistingTraceparent(t *testing.T) {
	var receivedTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Traceparent", "00-existing-trace-01")

	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if receivedTraceparent != "00-existing-trace-01" {
		t.Errorf("Traceparent = %q, want '00-existing-trace-01' (should preserve existing)", receivedTraceparent)
	}
}

// TestClientIPNoPortCov covers clientIP when RemoteAddr has no port.
func TestClientIPNoPortCov(t *testing.T) {
	ip := clientIP(&http.Request{RemoteAddr: "10.0.0.1"})
	if ip != "10.0.0.1" {
		t.Errorf("clientIP = %q, want 10.0.0.1", ip)
	}
}

// TestIsRetryableNetErrorCov covers isRetryableError with a net.Error.
func TestIsRetryableNetErrorCov(t *testing.T) {
	if isRetryableError(nil) {
		t.Error("nil should not be retryable")
	}

	// Test with string-based error matching
	err := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host"}}
	if !isRetryableError(err) {
		t.Error("net.Error should be retryable")
	}
}

// TestServeContextCancelledNotDeadline covers the ctx.Err() != nil path
// that is NOT DeadlineExceeded (lines 185-188).
func TestServeContextCancelledNotDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	ctx2, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req := httptest.NewRequest("GET", "/cancel", nil)
	req = req.WithContext(ctx2)

	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Timeouts: config.ProxyTimeouts{Read: config.Duration{Duration: 10 * time.Second}},
		},
	}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 && rec.Code != 504 {
		t.Errorf("status = %d, want 502 or 504", rec.Code)
	}
}

// TestServePostWithNilBody covers the nil body path (line 69-77).
func TestServePostWithNilBody(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/no-body", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if receivedBody != "" {
		t.Errorf("body = %q, want empty", receivedBody)
	}
}

// TestServeReadBodyError covers the io.ReadAll body error path (line 72-76).
func TestServeReadBodyError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("POST", "/error-body", &errBodyReader{})
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for body read error", rec.Code)
	}
}

// errBodyReader is a reader that always returns an error.
type errBodyReader struct{}

func (r *errBodyReader) Read(p []byte) (int, error) {
	return 0, net.ErrClosed
}

// TestForwardedProtoHTTPSCov covers forwardedProto when IsHTTPS is true.
func TestForwardedProtoHTTPSCov(t *testing.T) {
	ctx := &router.RequestContext{IsHTTPS: true}
	if got := forwardedProto(ctx); got != "https" {
		t.Errorf("forwardedProto = %q, want https", got)
	}
}
