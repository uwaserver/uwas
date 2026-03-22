package fastcgi

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// TestGetClientCaching verifies sync.Map caching in getClient.
func TestGetClientCaching(t *testing.T) {
	log := logger.New("error", "text")
	h := New(log)

	// Get a client for an address
	c1 := h.getClient("tcp:127.0.0.1:9999")
	c2 := h.getClient("tcp:127.0.0.1:9999")

	// Should return the same client instance (cached)
	if c1 != c2 {
		t.Error("getClient should return the same instance for the same address")
	}

	// Different address should return a different client
	c3 := h.getClient("tcp:127.0.0.1:9998")
	if c1 == c3 {
		t.Error("getClient should return different instances for different addresses")
	}
}

// mockFCGIServer starts a mock FastCGI server that accepts one connection,
// reads the request, and sends back a response with the given headers and body.
func mockFCGIServer(t *testing.T, ln net.Listener, responseHeaders, responseBody string, wg *sync.WaitGroup) {
	t.Helper()
	defer wg.Done()

	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()

	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	// Read all incoming records until we get an empty STDIN
	for {
		rec, err := fastcgi.ReadRecord(br)
		if err != nil {
			return
		}
		if rec.Type == fastcgi.TypeStdin && rec.ContentLength == 0 {
			break
		}
	}

	requestID := uint16(1)

	// Write the response (headers + blank line + body as FastCGI stdout)
	stdout := responseHeaders + "\r\n\r\n" + responseBody
	if err := fastcgi.WriteRecord(bw, fastcgi.TypeStdout, requestID, []byte(stdout)); err != nil {
		t.Errorf("mock write stdout: %v", err)
		return
	}
	fastcgi.WriteRecord(bw, fastcgi.TypeStdout, requestID, nil)

	// END_REQUEST
	endBody := make([]byte, 8)
	binary.BigEndian.PutUint32(endBody[0:4], 0) // app status 0
	fastcgi.WriteRecord(bw, fastcgi.TypeEndRequest, requestID, endBody)

	bw.Flush()
}

// TestServeWithMockFCGI tests the Serve method with a mock FastCGI server.
func TestServeWithMockFCGI(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	responseHeaders := "Status: 200 OK\r\nContent-Type: text/html\r\nX-PHP: yes"
	responseBody := "<h1>Hello from PHP</h1>"

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, responseHeaders, responseBody, &wg)

	log := logger.New("error", "text")
	h := New(log)

	// Inject the client directly into the handler's sync.Map
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/index.php"
	ctx.OriginalURI = "/index.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Hello from PHP") {
		t.Errorf("body = %q, should contain 'Hello from PHP'", body)
	}
	if rec.Header().Get("X-Php") != "yes" {
		t.Errorf("X-PHP header = %q, want yes", rec.Header().Get("X-Php"))
	}
}

// TestServeWithStderr verifies stderr is logged without breaking the response.
func TestServeWithStderr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()

		br := bufio.NewReader(c)
		bw := bufio.NewWriter(c)

		for {
			rec, err := fastcgi.ReadRecord(br)
			if err != nil {
				return
			}
			if rec.Type == fastcgi.TypeStdin && rec.ContentLength == 0 {
				break
			}
		}

		requestID := uint16(1)

		stdout := "Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nOK"
		fastcgi.WriteRecord(bw, fastcgi.TypeStdout, requestID, []byte(stdout))
		fastcgi.WriteRecord(bw, fastcgi.TypeStdout, requestID, nil)

		// Send stderr
		fastcgi.WriteRecord(bw, fastcgi.TypeStderr, requestID, []byte("PHP Warning: something"))
		fastcgi.WriteRecord(bw, fastcgi.TypeStderr, requestID, nil)

		endBody := make([]byte, 8)
		fastcgi.WriteRecord(bw, fastcgi.TypeEndRequest, requestID, endBody)
		bw.Flush()
	}()

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/test.php"
	ctx.OriginalURI = "/test.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestServeDialFailure tests Serve when the FastCGI backend is unreachable.
func TestServeDialFailure(t *testing.T) {
	log := logger.New("error", "text")
	h := New(log)

	// Use an address that will fail to connect
	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: "tcp:127.0.0.1:1",
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	reqCtx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	req = req.WithContext(reqCtx)

	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/index.php"
	ctx.OriginalURI = "/index.php"

	h.Serve(ctx, domain)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for dial failure", rec.Code)
	}
}

// TestServeWithRequestBody tests Serve with a POST body.
func TestServeWithRequestBody(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	responseHeaders := "Status: 200 OK\r\nContent-Type: application/json"
	responseBody := `{"received":true}`

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, responseHeaders, responseBody, &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	bodyStr := `{"name":"test"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api.php", strings.NewReader(bodyStr))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(bodyStr))

	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/api.php"
	ctx.OriginalURI = "/api.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `{"received":true}`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}
