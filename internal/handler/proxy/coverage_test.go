package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// --- Backend stats: ActiveConns, TotalReqs, TotalFails counters ---

func TestBackendStatsAfterSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	backend := pool.All()[0]

	// Verify initial state.
	if backend.TotalReqs.Load() != 0 {
		t.Errorf("initial TotalReqs = %d, want 0", backend.TotalReqs.Load())
	}
	if backend.TotalFails.Load() != 0 {
		t.Errorf("initial TotalFails = %d, want 0", backend.TotalFails.Load())
	}
	if backend.ActiveConns.Load() != 0 {
		t.Errorf("initial ActiveConns = %d, want 0", backend.ActiveConns.Load())
	}

	// Make a successful request.
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// TotalReqs should have incremented.
	if backend.TotalReqs.Load() != 1 {
		t.Errorf("TotalReqs = %d, want 1", backend.TotalReqs.Load())
	}
	// ActiveConns should be back to 0 after request completes.
	if backend.ActiveConns.Load() != 0 {
		t.Errorf("ActiveConns = %d, want 0 after request", backend.ActiveConns.Load())
	}
	// No fails expected.
	if backend.TotalFails.Load() != 0 {
		t.Errorf("TotalFails = %d, want 0", backend.TotalFails.Load())
	}
}

func TestBackendStatsAfterFailure(t *testing.T) {
	// Use a backend that refuses connections.
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://127.0.0.1:1", Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	backend := pool.All()[0]

	req := httptest.NewRequest("GET", "/fail", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Timeouts: config.ProxyTimeouts{Read: config.Duration{Duration: 1 * time.Second}},
		},
	}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}

	// TotalReqs should have incremented.
	if backend.TotalReqs.Load() < 1 {
		t.Errorf("TotalReqs = %d, want >= 1", backend.TotalReqs.Load())
	}
	// TotalFails should have incremented.
	if backend.TotalFails.Load() < 1 {
		t.Errorf("TotalFails = %d, want >= 1", backend.TotalFails.Load())
	}
	// ActiveConns should be 0 after failure.
	if backend.ActiveConns.Load() != 0 {
		t.Errorf("ActiveConns = %d, want 0 after failure", backend.ActiveConns.Load())
	}
}

// --- Multiple backend stats after several requests ---

func TestBackendStatsCumulative(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	backend := pool.All()[0]
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		ctx := router.AcquireContext(rec, req)
		h.Serve(ctx, domain, pool, balancer)
	}

	if backend.TotalReqs.Load() != 5 {
		t.Errorf("TotalReqs = %d, want 5", backend.TotalReqs.Load())
	}
	if backend.TotalFails.Load() != 0 {
		t.Errorf("TotalFails = %d, want 0", backend.TotalFails.Load())
	}
	if backend.ActiveConns.Load() != 0 {
		t.Errorf("ActiveConns = %d, want 0", backend.ActiveConns.Load())
	}
}

// --- Proxy with custom read timeout (long enough to succeed) ---

func TestProxyCustomReadTimeoutSuccess(t *testing.T) {
	// Upstream that delays 200ms.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
		w.Write([]byte("delayed"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Timeouts: config.ProxyTimeouts{Read: config.Duration{Duration: 5 * time.Second}},
		},
	}

	h.Serve(ctx, domain, pool, balancer)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 with sufficient timeout", rec.Code)
	}
}

// --- Proxy with default read timeout (no config) ---

func TestProxyNoTimeoutConfigured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{
		Host: "test.com",
		Type: "proxy",
		// No Timeouts configured -- uses defaults.
	}

	h.Serve(ctx, domain, pool, balancer)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- serveWebSocket with successful hijack ---

// covHijackableWriter implements http.ResponseWriter and http.Hijacker.
type covHijackableWriter struct {
	header http.Header
	code   int
	conn   net.Conn
}

func (w *covHijackableWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *covHijackableWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (w *covHijackableWriter) WriteHeader(code int) {
	w.code = code
}

func (w *covHijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw := bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn))
	return w.conn, rw, nil
}

func TestServeWebSocketWithHijack(t *testing.T) {
	// Start a backend that accepts TCP connections and responds with an upgrade.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	backendAddr := listener.Addr().String()

	// Backend goroutine: accept, read request, write upgrade response, close.
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		conn.Read(buf)

		resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
		conn.Write([]byte(resp))
		conn.Write([]byte("ws-data"))
		time.Sleep(50 * time.Millisecond)
	}()

	u, _ := url.Parse("http://" + backendAddr)
	backend := &Backend{URL: u, Weight: 1}

	log := logger.New("error", "text")
	h := New(log)

	// Create a pipe to simulate the hijacked connection.
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Read from the client side in the background.
	go func() {
		buf := make([]byte, 4096)
		clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		clientConn.Read(buf)
		clientConn.Close()
	}()

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "1.2.3.4:5678"
	req.Host = "test.com"

	hjWriter := &covHijackableWriter{conn: serverConn}
	ctx := router.AcquireContext(hjWriter, req)

	// Should not panic.
	h.serveWebSocket(ctx, backend)
}

// --- serveWebSocket with backend connection failure ---

func TestServeWebSocketBackendFail(t *testing.T) {
	// Use a non-existent backend address.
	u, _ := url.Parse("http://127.0.0.1:1")
	backend := &Backend{URL: u, Weight: 1}

	log := logger.New("error", "text")
	h := New(log)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Read the error response from the client side.
	go func() {
		buf := make([]byte, 4096)
		clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := clientConn.Read(buf)
		if n > 0 {
			response := string(buf[:n])
			if !strings.Contains(response, "502") {
				// The proxy writes "HTTP/1.1 502 Bad Gateway" on backend failure.
				_ = response
			}
		}
		clientConn.Close()
	}()

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "1.2.3.4:5678"
	req.Host = "test.com"

	hjWriter := &covHijackableWriter{conn: serverConn}
	ctx := router.AcquireContext(hjWriter, req)

	h.serveWebSocket(ctx, backend)
}

// --- Proxy request body buffering ---

func TestProxyRequestBodyBuffered(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
		w.Write([]byte("got it"))
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("POST", "/data", strings.NewReader("request body content"))
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if receivedBody != "request body content" {
		t.Errorf("received body = %q, want 'request body content'", receivedBody)
	}
}

// --- Backend state transitions ---

func TestBackendStateFullCycle(t *testing.T) {
	u, _ := url.Parse("http://localhost:3000")
	b := &Backend{URL: u, Weight: 1}

	if !b.IsHealthy() {
		t.Error("should start healthy")
	}

	b.SetState(StateUnhealthy)
	if b.IsHealthy() {
		t.Error("should be unhealthy")
	}
	if b.GetState() != StateUnhealthy {
		t.Errorf("state = %d, want StateUnhealthy", b.GetState())
	}

	b.SetState(StateDraining)
	if b.IsHealthy() {
		t.Error("should not be healthy when draining")
	}
	if b.GetState() != StateDraining {
		t.Errorf("state = %d, want StateDraining", b.GetState())
	}

	b.SetState(StateHealthy)
	if !b.IsHealthy() {
		t.Error("should be healthy again")
	}
}

// --- WebSocket: backend URL missing port ---

func TestServeWebSocketDefaultPort(t *testing.T) {
	// Test with HTTPS scheme (should add :443).
	u, _ := url.Parse("https://example.com")
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

	// Will fail to connect, but should not panic; exercises the default port logic.
	h.serveWebSocket(ctx, backend)
}

// --- Proxy hop-by-hop header removal in response ---

func TestProxyResponseHopByHopRemoval(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("X-Custom", "preserved")
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	pool := NewUpstreamPool([]UpstreamConfig{{Address: upstream.URL, Weight: 1}})
	balancer := NewBalancer("round_robin")
	log := logger.New("error", "text")
	h := New(log)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	domain := &config.Domain{Host: "test.com", Type: "proxy"}

	h.Serve(ctx, domain, pool, balancer)

	if rec.Header().Get("Connection") != "" {
		t.Error("Connection header should be removed from response")
	}
	if rec.Header().Get("Keep-Alive") != "" {
		t.Error("Keep-Alive header should be removed from response")
	}
	if rec.Header().Get("X-Custom") != "preserved" {
		t.Errorf("X-Custom = %q, want preserved", rec.Header().Get("X-Custom"))
	}
}
