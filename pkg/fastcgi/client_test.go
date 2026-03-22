package fastcgi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewClient / Client.Close
// ---------------------------------------------------------------------------

func TestNewClientCreatesPool(t *testing.T) {
	c := NewClient(PoolConfig{Address: "tcp:127.0.0.1:9000"})
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.pool == nil {
		t.Fatal("pool field is nil")
	}
	// Close should not panic even when no connections were created.
	c.Close()
}

// ---------------------------------------------------------------------------
// NewPool with custom config values
// ---------------------------------------------------------------------------

func TestNewPoolCustomConfig(t *testing.T) {
	cfg := PoolConfig{
		Address:     "unix:/tmp/test.sock",
		MaxIdle:     5,
		MaxOpen:     20,
		MaxLifetime: 2 * time.Minute,
	}
	p := NewPool(cfg)
	defer p.Close()

	if p.network != "unix" {
		t.Errorf("network = %q, want unix", p.network)
	}
	if p.address != "/tmp/test.sock" {
		t.Errorf("address = %q, want /tmp/test.sock", p.address)
	}
	if p.maxIdle != 5 {
		t.Errorf("maxIdle = %d, want 5", p.maxIdle)
	}
	if p.maxOpen != 20 {
		t.Errorf("maxOpen = %d, want 20", p.maxOpen)
	}
	if p.maxLife != 2*time.Minute {
		t.Errorf("maxLife = %v, want 2m", p.maxLife)
	}
}

// ---------------------------------------------------------------------------
// parseAddress: additional edge-case subtests
// ---------------------------------------------------------------------------

func TestParseAddressSubtests(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantNetwork string
		wantAddress string
	}{
		{"unix prefix", "unix:/var/run/php-fpm.sock", "unix", "/var/run/php-fpm.sock"},
		{"tcp prefix", "tcp:127.0.0.1:9000", "tcp", "127.0.0.1:9000"},
		{"bare unix path", "/var/run/php-fpm.sock", "unix", "/var/run/php-fpm.sock"},
		{"bare tcp address", "127.0.0.1:9000", "tcp", "127.0.0.1:9000"},
		{"tcp localhost", "tcp:localhost:9000", "tcp", "localhost:9000"},
		{"unix tmp", "unix:/tmp/test.sock", "unix", "/tmp/test.sock"},
		{"bare hostname", "myhost:8080", "tcp", "myhost:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, address := parseAddress(tt.input)
			if network != tt.wantNetwork {
				t.Errorf("parseAddress(%q) network = %q, want %q", tt.input, network, tt.wantNetwork)
			}
			if address != tt.wantAddress {
				t.Errorf("parseAddress(%q) address = %q, want %q", tt.input, address, tt.wantAddress)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mockFCGIServerFull is a mock FastCGI server that supports stdout, stderr,
// and a configurable app status.
// ---------------------------------------------------------------------------

func mockFCGIServerFull(
	t *testing.T,
	ln net.Listener,
	stdoutPayload string,
	stderrPayload string,
	appStatus uint32,
	wg *sync.WaitGroup,
) {
	t.Helper()
	defer wg.Done()

	c, err := ln.Accept()
	if err != nil {
		t.Errorf("mock accept: %v", err)
		return
	}
	defer c.Close()

	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	// Read all incoming records until we get an empty STDIN.
	for {
		rec, err := ReadRecord(br)
		if err != nil {
			t.Errorf("mock ReadRecord: %v", err)
			return
		}
		if rec.Type == TypeStdin && rec.ContentLength == 0 {
			break
		}
	}

	requestID := uint16(1)

	// STDOUT
	if len(stdoutPayload) > 0 {
		if err := WriteRecord(bw, TypeStdout, requestID, []byte(stdoutPayload)); err != nil {
			t.Errorf("mock write stdout: %v", err)
			return
		}
	}
	WriteRecord(bw, TypeStdout, requestID, nil)

	// STDERR
	if len(stderrPayload) > 0 {
		WriteRecord(bw, TypeStderr, requestID, []byte(stderrPayload))
		WriteRecord(bw, TypeStderr, requestID, nil)
	}

	// END_REQUEST
	endBody := make([]byte, 8)
	binary.BigEndian.PutUint32(endBody[0:4], appStatus)
	WriteRecord(bw, TypeEndRequest, requestID, endBody)

	bw.Flush()
}

// ---------------------------------------------------------------------------
// Client.Execute with stderr and non-zero app status
// ---------------------------------------------------------------------------

func TestClientExecuteWithStderrAndAppStatus(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 500 Internal Server Error\r\nContent-Type: text/html\r\n\r\nError"
	stderrPayload := "PHP Fatal error: something went wrong"

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServerFull(t, ln, stdoutPayload, stderrPayload, 1, &wg)

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 2,
		MaxOpen: 2,
	})
	defer client.Close()

	env := map[string]string{
		"SCRIPT_FILENAME": "/var/www/bad.php",
		"REQUEST_METHOD":  "GET",
	}

	resp, err := client.Execute(context.Background(), env, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	if resp.AppStatus != 1 {
		t.Errorf("AppStatus = %d, want 1", resp.AppStatus)
	}

	stderr := string(resp.Stderr())
	if !strings.Contains(stderr, "PHP Fatal error") {
		t.Errorf("stderr = %q, want it to contain 'PHP Fatal error'", stderr)
	}

	stdout := string(resp.Stdout())
	if !strings.Contains(stdout, "Error") {
		t.Errorf("stdout = %q, want it to contain 'Error'", stdout)
	}
}

// ---------------------------------------------------------------------------
// Client.Execute: dial error through the Client API
// ---------------------------------------------------------------------------

func TestClientExecuteDialFailure(t *testing.T) {
	client := NewClient(PoolConfig{
		Address: "tcp:127.0.0.1:1", // port 1 is almost never open
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Execute(ctx, map[string]string{}, nil)
	if err == nil {
		t.Fatal("expected error when connecting to unreachable port, got nil")
	}
	if !strings.Contains(err.Error(), "get connection") {
		t.Errorf("error = %v, want it to mention 'get connection'", err)
	}
}

// ---------------------------------------------------------------------------
// Client.Execute roundtrip + Response.ParseHTTP with status 201
// ---------------------------------------------------------------------------

func TestClientExecuteRoundtripParseHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 201 Created\r\nContent-Type: text/plain\r\nX-Custom: hello\r\n\r\nBody here"

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServerFull(t, ln, stdoutPayload, "", 0, &wg)

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	resp, err := client.Execute(context.Background(), map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "GET",
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	statusCode, headers, bodyReader := resp.ParseHTTP()

	if statusCode != 201 {
		t.Errorf("statusCode = %d, want 201", statusCode)
	}
	if ct := headers.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if xc := headers.Get("X-Custom"); xc != "hello" {
		t.Errorf("X-Custom = %q, want hello", xc)
	}
	if headers.Get("Status") != "" {
		t.Error("Status pseudo-header should have been removed")
	}

	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		t.Fatalf("ReadAll body: %v", err)
	}
	if string(bodyBytes) != "Body here" {
		t.Errorf("body = %q, want 'Body here'", bodyBytes)
	}
}

// ---------------------------------------------------------------------------
// Multiple sequential Execute calls: connection reuse through the pool
// ---------------------------------------------------------------------------

// Connection reuse is tested implicitly via TestPoolGetPutLifecycle.

// ---------------------------------------------------------------------------
// Pool: Get, Put, Stats lifecycle
// ---------------------------------------------------------------------------

func TestPoolGetPutLifecycle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 4,
		MaxOpen: 8,
	})
	defer p.Close()

	ctx := context.Background()

	// Initially empty.
	active, idle := p.Stats()
	if active != 0 || idle != 0 {
		t.Errorf("initial stats: active=%d, idle=%d; want 0, 0", active, idle)
	}

	// Get a connection.
	c1, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	active, _ = p.Stats()
	if active != 1 {
		t.Errorf("after Get: active=%d, want 1", active)
	}

	// Put it back.
	p.Put(c1)
	active, idle = p.Stats()
	if active != 1 || idle != 1 {
		t.Errorf("after Put: active=%d, idle=%d; want 1, 1", active, idle)
	}

	// Get again: should reuse the idle connection.
	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get reuse: %v", err)
	}
	_, idle = p.Stats()
	if idle != 0 {
		t.Errorf("after reuse Get: idle=%d, want 0", idle)
	}
	p.Put(c2)
}

// ---------------------------------------------------------------------------
// Pool: Discard reduces active count
// ---------------------------------------------------------------------------

func TestPoolDiscardReducesActive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 4,
		MaxOpen: 8,
	})
	defer p.Close()

	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	active, _ := p.Stats()
	if active != 1 {
		t.Errorf("before Discard: active=%d, want 1", active)
	}

	p.Discard(c)

	active, _ = p.Stats()
	if active != 0 {
		t.Errorf("after Discard: active=%d, want 0", active)
	}
}

// ---------------------------------------------------------------------------
// Pool: Close drains idle connections
// ---------------------------------------------------------------------------

func TestPoolCloseDrainsIdle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 4,
		MaxOpen: 8,
	})

	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Put(c)

	_, idle := p.Stats()
	if idle != 1 {
		t.Errorf("before Close: idle=%d, want 1", idle)
	}

	p.Close() // should not panic and should drain the idle channel
}

// ---------------------------------------------------------------------------
// Pool: cancelled context when pool is exhausted
// ---------------------------------------------------------------------------

func TestPoolGetCancelledContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer p.Close()

	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = p.Get(cancelCtx)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}

	p.Put(c)
}

// ---------------------------------------------------------------------------
// Pool: Put and Discard nil are safe no-ops
// ---------------------------------------------------------------------------

func TestPoolPutAndDiscardNil(t *testing.T) {
	p := NewPool(PoolConfig{Address: "tcp:127.0.0.1:9000"})
	defer p.Close()
	// Neither should panic.
	p.Put(nil)
	p.Discard(nil)
}

// ---------------------------------------------------------------------------
// Pool: Put overflow drops the connection
// ---------------------------------------------------------------------------

func TestPoolPutOverflowDropsConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 4,
	})
	defer p.Close()

	ctx := context.Background()
	c1, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get c1: %v", err)
	}
	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get c2: %v", err)
	}

	p.Put(c1) // goes to idle (maxIdle=1)
	p.Put(c2) // overflow -> closed

	active, idle := p.Stats()
	if idle != 1 {
		t.Errorf("idle = %d, want 1", idle)
	}
	if active != 1 {
		t.Errorf("active = %d, want 1 (overflow was closed)", active)
	}
}

// ---------------------------------------------------------------------------
// Pool: stale connection eviction on Get
// ---------------------------------------------------------------------------

func TestPoolStaleEviction(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address:     "tcp:" + ln.Addr().String(),
		MaxIdle:     4,
		MaxOpen:     8,
		MaxLifetime: 50 * time.Millisecond,
	})
	defer p.Close()

	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Put(c)

	time.Sleep(100 * time.Millisecond)

	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get after stale: %v", err)
	}
	if time.Since(c2.createdAt) > 50*time.Millisecond {
		t.Error("expected a freshly created connection after stale eviction")
	}
	p.Put(c2)
}

// ---------------------------------------------------------------------------
// Client.Execute with large stdin (multiple reads)
// ---------------------------------------------------------------------------

func TestClientExecuteWithLargeStdin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nGot it"

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServerFull(t, ln, stdoutPayload, "", 0, &wg)

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 2,
		MaxOpen: 2,
	})
	defer client.Close()

	// Send a large body that requires multiple reads
	largeBody := strings.Repeat("x", 100000) // 100KB
	env := map[string]string{
		"SCRIPT_FILENAME": "/var/www/upload.php",
		"REQUEST_METHOD":  "POST",
		"CONTENT_LENGTH":  "100000",
	}

	resp, err := client.Execute(context.Background(), env, strings.NewReader(largeBody))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	if resp.AppStatus != 0 {
		t.Errorf("AppStatus = %d, want 0", resp.AppStatus)
	}
	stdout := string(resp.Stdout())
	if !strings.Contains(stdout, "Got it") {
		t.Errorf("stdout = %q, want it to contain 'Got it'", stdout)
	}
}

// ---------------------------------------------------------------------------
// Pool: Get stale eviction with MaxLifetime
// ---------------------------------------------------------------------------

func TestPoolStaleEvictionWithMaxLifetime(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address:     "tcp:" + ln.Addr().String(),
		MaxIdle:     4,
		MaxOpen:     8,
		MaxLifetime: 10 * time.Millisecond,
	})
	defer p.Close()

	ctx := context.Background()
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Put(c)

	// Wait for usedAt to become stale (>30s idle check)
	// MaxLifetime is 10ms, so the connection should be evicted
	time.Sleep(50 * time.Millisecond)

	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get after stale: %v", err)
	}
	// Should be a new connection since the old one was evicted
	if time.Since(c2.createdAt) > 20*time.Millisecond {
		t.Error("expected a fresh connection")
	}
	p.Put(c2)
}

// ---------------------------------------------------------------------------
// Response: empty stdout and stderr
// ---------------------------------------------------------------------------

func TestResponseEmptyStdoutStderr(t *testing.T) {
	r := &Response{}
	if len(r.Stdout()) != 0 {
		t.Errorf("Stdout() on empty = %q, want empty", r.Stdout())
	}
	if len(r.Stderr()) != 0 {
		t.Errorf("Stderr() on empty = %q, want empty", r.Stderr())
	}
}

// ---------------------------------------------------------------------------
// Response.ParseHTTP: large body
// ---------------------------------------------------------------------------

func TestResponseParseHTTPLargeBody(t *testing.T) {
	body := strings.Repeat("ABCDEFGHIJ", 1000) // 10 KB
	r := &Response{}
	r.stdout.WriteString("Status: 200 OK\r\nContent-Type: application/octet-stream\r\n\r\n" + body)

	statusCode, headers, bodyReader := r.ParseHTTP()

	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200", statusCode)
	}
	if headers.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q", headers.Get("Content-Type"))
	}

	got, err := io.ReadAll(bodyReader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != body {
		t.Errorf("body length = %d, want %d", len(got), len(body))
	}
}

// ---------------------------------------------------------------------------
// Verify the mock server reads PARAMS sent by Client.Execute
// ---------------------------------------------------------------------------

func TestClientExecuteParamsReachMock(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var receivedParams map[string]string
	var mu sync.Mutex
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

		var paramData bytes.Buffer

		for {
			rec, err := ReadRecord(br)
			if err != nil {
				return
			}
			switch rec.Type {
			case TypeParams:
				if rec.ContentLength > 0 {
					paramData.Write(rec.Content)
				}
			case TypeStdin:
				if rec.ContentLength == 0 {
					goto respond
				}
			}
		}

	respond:
		params, err := DecodeParams(paramData.Bytes())
		if err == nil {
			mu.Lock()
			receivedParams = params
			mu.Unlock()
		}

		WriteRecord(bw, TypeStdout, 1, []byte("Content-Type: text/plain\r\n\r\nok"))
		WriteRecord(bw, TypeStdout, 1, nil)
		endBody := make([]byte, 8)
		WriteRecord(bw, TypeEndRequest, 1, endBody)
		bw.Flush()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	env := map[string]string{
		"SCRIPT_FILENAME": "/var/www/hello.php",
		"REQUEST_METHOD":  "POST",
		"CONTENT_TYPE":    "text/plain",
	}

	_, err = client.Execute(context.Background(), env, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	for k, v := range env {
		if receivedParams[k] != v {
			t.Errorf("param %q = %q, want %q", k, receivedParams[k], v)
		}
	}
}

// ---------------------------------------------------------------------------
// Client.Execute with stdin body
// ---------------------------------------------------------------------------

func TestClientExecuteWithStdinBody(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 200 OK\r\nContent-Type: application/json\r\n\r\n{\"ok\":true}"

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServerFull(t, ln, stdoutPayload, "", 0, &wg)

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 2,
		MaxOpen: 2,
	})
	defer client.Close()

	body := strings.NewReader(`{"name":"test"}`)
	env := map[string]string{
		"SCRIPT_FILENAME": "/var/www/api.php",
		"REQUEST_METHOD":  "POST",
		"CONTENT_TYPE":    "application/json",
		"CONTENT_LENGTH":  "15",
	}

	resp, err := client.Execute(context.Background(), env, body)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	stdout := string(resp.Stdout())
	if !strings.Contains(stdout, `{"ok":true}`) {
		t.Errorf("stdout = %q, want it to contain '{\"ok\":true}'", stdout)
	}
}

// ---------------------------------------------------------------------------
// Response.ParseHTTP: no Status header defaults to 200
// ---------------------------------------------------------------------------

func TestResponseParseHTTPDefaultsTo200(t *testing.T) {
	r := &Response{}
	r.stdout.WriteString("Content-Type: text/html\r\n\r\n<h1>Hi</h1>")

	statusCode, headers, bodyReader := r.ParseHTTP()

	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200 (default)", statusCode)
	}
	if ct := headers.Get("Content-Type"); ct != "text/html" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body, _ := io.ReadAll(bodyReader)
	if string(body) != "<h1>Hi</h1>" {
		t.Errorf("body = %q, want '<h1>Hi</h1>'", body)
	}
}

// ---------------------------------------------------------------------------
// Response.ParseHTTP: malformed response falls back gracefully
// ---------------------------------------------------------------------------

func TestResponseParseHTTPMalformedFallback(t *testing.T) {
	r := &Response{}
	r.stdout.WriteString("this is not http at all")

	statusCode, _, _ := r.ParseHTTP()

	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200 (fallback)", statusCode)
	}
}

// ---------------------------------------------------------------------------
// Response.Stdout / Response.Stderr basic accessors
// ---------------------------------------------------------------------------

func TestResponseAccessors(t *testing.T) {
	r := &Response{}
	r.stdout.WriteString("stdout data")
	r.stderr.WriteString("stderr data")

	if !bytes.Equal(r.Stdout(), []byte("stdout data")) {
		t.Errorf("Stdout() = %q", r.Stdout())
	}
	if !bytes.Equal(r.Stderr(), []byte("stderr data")) {
		t.Errorf("Stderr() = %q", r.Stderr())
	}
}

// ---------------------------------------------------------------------------
// Client.Execute: stdin reader that returns an error
// ---------------------------------------------------------------------------

type errReader struct {
	data    []byte
	pos     int
	failAt  int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= r.failAt {
		return 0, errors.New("simulated read error")
	}
	n := copy(p, r.data[r.pos:])
	if r.pos+n >= r.failAt {
		n = r.failAt - r.pos
		r.pos = r.failAt
		return n, nil
	}
	r.pos += n
	return n, nil
}

func TestClientExecuteStdinReadError(t *testing.T) {
	// We connect to a mock server but send a failing stdin reader.
	// The client should detect the read error and return it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Mock server that accepts but just reads and discards
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(io.Discard, c)
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	// Create reader that errors after some data
	failingReader := &errReader{
		data:   make([]byte, 10000),
		failAt: 5000,
	}

	_, err = client.Execute(context.Background(), map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "POST",
	}, failingReader)
	if err == nil {
		t.Fatal("expected error from failing stdin reader")
	}
	if !strings.Contains(err.Error(), "read stdin") {
		t.Errorf("error = %v, want it to mention 'read stdin'", err)
	}
}

// ---------------------------------------------------------------------------
// Pool: Get returns idle connection from wait path when pool is exhausted
// ---------------------------------------------------------------------------

func TestPoolGetWaitForIdle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := NewPool(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 2,
		MaxOpen: 1, // only 1 connection allowed
	})
	defer p.Close()

	ctx := context.Background()

	// Get the only connection
	c1, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Start a goroutine that waits for an idle connection
	done := make(chan error, 1)
	go func() {
		ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c2, err := p.Get(ctx2)
		if err != nil {
			done <- err
			return
		}
		p.Put(c2)
		done <- nil
	}()

	// Return the first connection after a short delay
	time.Sleep(50 * time.Millisecond)
	p.Put(c1)

	// The goroutine should succeed
	err = <-done
	if err != nil {
		t.Fatalf("Get wait for idle: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Response.ParseHTTP: Status header with short code
// ---------------------------------------------------------------------------

func TestResponseParseHTTPShortStatus(t *testing.T) {
	r := &Response{}
	r.stdout.WriteString("Status: 20\r\nContent-Type: text/plain\r\n\r\nbody")

	statusCode, _, _ := r.ParseHTTP()
	// "20" is only 2 chars, code >= 3 check fails, so statusCode stays 0 -> default 200
	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200 (default for short status)", statusCode)
	}
}

// ---------------------------------------------------------------------------
// Pool: NewPool with zero config values (defaults)
// ---------------------------------------------------------------------------

func TestNewPoolDefaults(t *testing.T) {
	p := NewPool(PoolConfig{
		Address: "tcp:127.0.0.1:9000",
	})
	defer p.Close()

	if p.maxIdle != 10 {
		t.Errorf("maxIdle = %d, want 10", p.maxIdle)
	}
	if p.maxOpen != 64 {
		t.Errorf("maxOpen = %d, want 64", p.maxOpen)
	}
	if p.maxLife != 5*time.Minute {
		t.Errorf("maxLife = %v, want 5m", p.maxLife)
	}
}

// ---------------------------------------------------------------------------
// Client.Execute: complete roundtrip with nil stdin and multiple records
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Client.Execute: server closes connection immediately (read record error)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Client.Execute: connection that closes immediately (write begin error)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Client.Execute: server that immediately closes causes write/flush error
// ---------------------------------------------------------------------------

func TestClientExecuteFlushError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Close immediately after accepting
		c.Close()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	// Give time for connection to be established and then closed
	time.Sleep(50 * time.Millisecond)

	// Send a large amount of data to ensure the write buffer overflows and fails
	largeBody := strings.NewReader(strings.Repeat("x", 1000000)) // 1MB
	_, err = client.Execute(context.Background(), map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "POST",
		"CONTENT_LENGTH":  "1000000",
	}, largeBody)
	if err == nil {
		t.Fatal("expected error when server closes connection")
	}
}

func TestClientExecuteServerClosesEarly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Read a bit, then close immediately
		buf := make([]byte, 100)
		c.Read(buf)
		c.Close()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	_, err = client.Execute(context.Background(), map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "GET",
	}, nil)
	if err == nil {
		t.Fatal("expected error when server closes connection early")
	}
}

// ---------------------------------------------------------------------------
// Client.Execute: server drops connection after accepting but before response
// ---------------------------------------------------------------------------

func TestClientExecuteServerDropsAfterStdin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Read all data then close without sending response
		br := bufio.NewReader(c)
		for {
			rec, err := ReadRecord(br)
			if err != nil {
				c.Close()
				return
			}
			if rec.Type == TypeStdin && rec.ContentLength == 0 {
				// Got end of stdin, close without sending response
				c.Close()
				return
			}
		}
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	_, err = client.Execute(context.Background(), map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "GET",
	}, nil)
	if err == nil {
		t.Fatal("expected error when server drops connection before response")
	}
	if !strings.Contains(err.Error(), "read record") {
		t.Errorf("error = %v, want 'read record'", err)
	}
}

func TestClientExecuteNilStdin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServerFull(t, ln,
		"Content-Type: text/plain\r\n\r\nOK",
		"", 0, &wg)

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	resp, err := client.Execute(context.Background(), map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "GET",
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	statusCode, _, bodyReader := resp.ParseHTTP()
	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200", statusCode)
	}
	body, _ := io.ReadAll(bodyReader)
	if string(body) != "OK" {
		t.Errorf("body = %q, want OK", body)
	}
}
