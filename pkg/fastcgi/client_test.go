package fastcgi

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// parseAddress
// ---------------------------------------------------------------------------

func TestParseAddress(t *testing.T) {
	tests := []struct {
		input       string
		wantNetwork string
		wantAddress string
	}{
		{"unix:/var/run/php-fpm.sock", "unix", "/var/run/php-fpm.sock"},
		{"tcp:127.0.0.1:9000", "tcp", "127.0.0.1:9000"},
		{"/var/run/php-fpm.sock", "unix", "/var/run/php-fpm.sock"},
		{"127.0.0.1:9000", "tcp", "127.0.0.1:9000"},
		{"tcp:localhost:9000", "tcp", "localhost:9000"},
		{"unix:/tmp/test.sock", "unix", "/tmp/test.sock"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
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
// Pool (NewPool, Get, Put, Discard, Close, Stats)
// ---------------------------------------------------------------------------

func TestNewPoolDefaults(t *testing.T) {
	cfg := PoolConfig{Address: "tcp:127.0.0.1:9000"}
	p := NewPool(cfg)
	defer p.Close()

	if p.network != "tcp" {
		t.Errorf("network = %q, want tcp", p.network)
	}
	if p.address != "127.0.0.1:9000" {
		t.Errorf("address = %q, want 127.0.0.1:9000", p.address)
	}
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

func TestNewPoolCustom(t *testing.T) {
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

// newLocalPool creates a Pool wired to a local TCP listener address.
func newLocalPool(addr string) *Pool {
	return NewPool(PoolConfig{
		Address:     "tcp:" + addr,
		MaxIdle:     4,
		MaxOpen:     8,
		MaxLifetime: 1 * time.Minute,
	})
}

func TestPoolGetPutStats(t *testing.T) {
	// Start a dummy TCP listener so dials succeed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Accept connections in the background so they don't hang.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Keep the connection open until the test closes the listener.
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p := newLocalPool(ln.Addr().String())
	defer p.Close()

	ctx := context.Background()

	// Stats should be zero initially.
	active, idle := p.Stats()
	if active != 0 || idle != 0 {
		t.Errorf("initial stats: active=%d, idle=%d; want 0, 0", active, idle)
	}

	// Get a connection.
	c1, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	active, idle = p.Stats()
	if active != 1 {
		t.Errorf("after Get: active=%d, want 1", active)
	}

	// Put it back.
	p.Put(c1)

	active, idle = p.Stats()
	if active != 1 {
		t.Errorf("after Put: active=%d, want 1", active)
	}
	if idle != 1 {
		t.Errorf("after Put: idle=%d, want 1", idle)
	}

	// Get again -- should reuse the idle connection.
	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get reuse: %v", err)
	}

	active, idle = p.Stats()
	if active != 1 {
		t.Errorf("after reuse Get: active=%d, want 1", active)
	}
	if idle != 0 {
		t.Errorf("after reuse Get: idle=%d, want 0", idle)
	}
	p.Put(c2)
}

func TestPoolDiscard(t *testing.T) {
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

	p := newLocalPool(ln.Addr().String())
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

func TestPoolDiscardNil(t *testing.T) {
	p := NewPool(PoolConfig{Address: "tcp:127.0.0.1:9000"})
	defer p.Close()
	// Should not panic.
	p.Discard(nil)
}

func TestPoolPutNil(t *testing.T) {
	p := NewPool(PoolConfig{Address: "tcp:127.0.0.1:9000"})
	defer p.Close()
	// Should not panic.
	p.Put(nil)
}

func TestPoolClose(t *testing.T) {
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

	p := newLocalPool(ln.Addr().String())

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

	p.Close()
	// After close the idle channel is drained and closed.
}

func TestPoolGetContextCancel(t *testing.T) {
	// Create a pool with maxOpen=1 so that a second Get blocks.
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
	// Consume the one allowed connection.
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// A second Get with a cancelled context should fail.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = p.Get(cancelCtx)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}

	p.Put(c)
}

// ---------------------------------------------------------------------------
// NewClient / Client.Close
// ---------------------------------------------------------------------------

func TestNewClientAndClose(t *testing.T) {
	c := NewClient(PoolConfig{Address: "tcp:127.0.0.1:9000"})
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.pool == nil {
		t.Fatal("pool is nil")
	}
	// Close should not panic even without active connections.
	c.Close()
}

// ---------------------------------------------------------------------------
// Mock FastCGI server
// ---------------------------------------------------------------------------

// mockFCGIServer accepts one connection on the given listener, reads a
// complete FastCGI request (BEGIN, PARAMS, STDIN), then writes back STDOUT
// and END_REQUEST records.
//
// stdoutPayload is sent as-is inside a FCGI_STDOUT record.
// stderrPayload, if non-empty, is sent inside a FCGI_STDERR record.
// appStatus is placed into the FCGI_END_REQUEST body.
func mockFCGIServer(
	t *testing.T,
	ln net.Listener,
	stdoutPayload string,
	stderrPayload string,
	appStatus uint32,
) {
	t.Helper()

	c, err := ln.Accept()
	if err != nil {
		t.Errorf("mock accept: %v", err)
		return
	}
	defer c.Close()

	// --- Read all incoming records until empty STDIN. ---
	gotBegin := false
	gotParamsEnd := false
	gotStdinEnd := false

	for !gotStdinEnd {
		rec, err := ReadRecord(c)
		if err != nil {
			t.Errorf("mock ReadRecord: %v", err)
			return
		}
		switch rec.Type {
		case TypeBeginRequest:
			gotBegin = true
		case TypeParams:
			if rec.ContentLength == 0 {
				gotParamsEnd = true
			}
		case TypeStdin:
			if rec.ContentLength == 0 {
				gotStdinEnd = true
			}
		}
	}

	if !gotBegin || !gotParamsEnd {
		t.Errorf("mock: missing expected records (begin=%v, paramsEnd=%v)", gotBegin, gotParamsEnd)
		return
	}

	requestID := uint16(1)

	// --- Send STDOUT ---
	if len(stdoutPayload) > 0 {
		if err := WriteRecord(c, TypeStdout, requestID, []byte(stdoutPayload)); err != nil {
			t.Errorf("mock WriteRecord stdout: %v", err)
			return
		}
	}
	// Empty stdout signals end of stdout.
	if err := WriteRecord(c, TypeStdout, requestID, nil); err != nil {
		t.Errorf("mock WriteRecord empty stdout: %v", err)
		return
	}

	// --- Send STDERR ---
	if len(stderrPayload) > 0 {
		if err := WriteRecord(c, TypeStderr, requestID, []byte(stderrPayload)); err != nil {
			t.Errorf("mock WriteRecord stderr: %v", err)
			return
		}
		if err := WriteRecord(c, TypeStderr, requestID, nil); err != nil {
			t.Errorf("mock WriteRecord empty stderr: %v", err)
			return
		}
	}

	// --- Send END_REQUEST ---
	endBody := make([]byte, 8)
	binary.BigEndian.PutUint32(endBody[0:4], appStatus)
	if err := WriteRecord(c, TypeEndRequest, requestID, endBody); err != nil {
		t.Errorf("mock WriteRecord end: %v", err)
		return
	}
}

// ---------------------------------------------------------------------------
// Client.Execute + Response helpers
// ---------------------------------------------------------------------------

func TestClientExecuteSimple(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nHello World"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mockFCGIServer(t, ln, stdoutPayload, "", 0)
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 2,
		MaxOpen: 2,
	})
	defer client.Close()

	env := map[string]string{
		"SCRIPT_FILENAME": "/var/www/index.php",
		"REQUEST_METHOD":  "GET",
		"REQUEST_URI":     "/",
	}

	resp, err := client.Execute(context.Background(), env, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	if resp.AppStatus != 0 {
		t.Errorf("AppStatus = %d, want 0", resp.AppStatus)
	}

	stdout := string(resp.Stdout())
	if !strings.Contains(stdout, "Hello World") {
		t.Errorf("stdout = %q, want it to contain 'Hello World'", stdout)
	}

	stderr := string(resp.Stderr())
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestClientExecuteWithStdin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 200 OK\r\nContent-Type: application/json\r\n\r\n{\"ok\":true}"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mockFCGIServer(t, ln, stdoutPayload, "", 0)
	}()

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

func TestClientExecuteWithStderr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 500 Internal Server Error\r\nContent-Type: text/html\r\n\r\nError"
	stderrPayload := "PHP Fatal error: something went wrong"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mockFCGIServer(t, ln, stdoutPayload, stderrPayload, 1)
	}()

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
}

func TestClientExecuteDialError(t *testing.T) {
	// Point at an address that no one is listening on.
	client := NewClient(PoolConfig{
		Address: "tcp:127.0.0.1:1", // port 1 is very unlikely to be open
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.Execute(ctx, map[string]string{}, nil)
	if err == nil {
		t.Fatal("expected error when connecting to closed port, got nil")
	}
}

// ---------------------------------------------------------------------------
// Response.ParseHTTP
// ---------------------------------------------------------------------------

func TestResponseParseHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stdoutPayload := "Status: 201 Created\r\nContent-Type: text/plain\r\nX-Custom: hello\r\n\r\nBody here"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mockFCGIServer(t, ln, stdoutPayload, "", 0)
	}()

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

	ct := headers.Get("Content-Type")
	if ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	xc := headers.Get("X-Custom")
	if xc != "hello" {
		t.Errorf("X-Custom = %q, want hello", xc)
	}

	// The Status pseudo-header should be removed.
	if headers.Get("Status") != "" {
		t.Error("Status header should have been removed")
	}

	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		t.Fatalf("ReadAll body: %v", err)
	}
	if string(bodyBytes) != "Body here" {
		t.Errorf("body = %q, want 'Body here'", bodyBytes)
	}
}

func TestResponseParseHTTPNoStatus(t *testing.T) {
	// Build a Response manually without a Status header.
	r := &Response{}
	r.stdout.WriteString("Content-Type: text/html\r\n\r\n<h1>Hi</h1>")

	statusCode, headers, bodyReader := r.ParseHTTP()

	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200 (default)", statusCode)
	}

	ct := headers.Get("Content-Type")
	if ct != "text/html" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body, _ := io.ReadAll(bodyReader)
	if string(body) != "<h1>Hi</h1>" {
		t.Errorf("body = %q, want '<h1>Hi</h1>'", body)
	}
}

func TestResponseParseHTTPMalformed(t *testing.T) {
	// Malformed content — ParseHTTP should not panic and return 200 status.
	r := &Response{}
	r.stdout.WriteString("this is not http at all")

	statusCode, _, _ := r.ParseHTTP()

	if statusCode != 200 {
		t.Errorf("statusCode = %d, want 200 (fallback)", statusCode)
	}
}

func TestResponseStdoutStderr(t *testing.T) {
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
// Pool: stale connection eviction
// ---------------------------------------------------------------------------

func TestPoolEvictsStaleConnection(t *testing.T) {
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
		MaxLifetime: 50 * time.Millisecond, // very short lifetime
	})
	defer p.Close()

	ctx := context.Background()

	// Get a connection and return it.
	c, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Put(c)

	// Wait for it to become stale.
	time.Sleep(100 * time.Millisecond)

	// Next Get should evict the stale connection and create a new one.
	c2, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get after stale: %v", err)
	}

	// The new connection should be different (fresh createdAt).
	if time.Since(c2.createdAt) > 50*time.Millisecond {
		t.Error("expected a freshly created connection after stale eviction")
	}
	p.Put(c2)
}

// ---------------------------------------------------------------------------
// Pool: Put when pool is full drops the connection
// ---------------------------------------------------------------------------

func TestPoolPutWhenFull(t *testing.T) {
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
		MaxIdle: 1, // only 1 idle slot
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

	active, _ := p.Stats()
	if active != 2 {
		t.Errorf("active = %d, want 2", active)
	}

	// Put both back; the second should be dropped because maxIdle=1.
	p.Put(c1)
	p.Put(c2)

	active, idle := p.Stats()
	if idle != 1 {
		t.Errorf("idle = %d, want 1 (maxIdle)", idle)
	}
	// active should be 1 because the second conn was closed on Put overflow.
	if active != 1 {
		t.Errorf("active = %d, want 1 (overflow conn was closed)", active)
	}
}
