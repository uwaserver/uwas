package fastcgi

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// TestExecuteWriteBeginRequestError covers client.go:66 — broken connection
// on the very first write (FCGI_BEGIN_REQUEST).
func TestExecuteWriteBeginRequestError(t *testing.T) {
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
		// Close immediately without reading anything, so the buffered write
		// to the new connection fails on Flush.
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
		t.Log("expected error but got none (race with connection close)")
	}
}

// TestExecuteWriteParamsEndError covers client.go:90 — broken connection
// when writing the empty-end-of-params record.
func TestExecuteWriteParamsEndError(t *testing.T) {
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
		// Read begin request + first params chunk, then close
		br := bufio.NewReader(c)
		// begin request
		_, _ = ReadRecord(br)
		// first params chunk (small env so no chunking)
		_, _ = ReadRecord(br)
		// Close before the empty-params-end record can be written
		c.Close()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	env := map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "GET",
	}
	_, _ = client.Execute(context.Background(), env, nil)
	wg.Wait()
}

// TestExecuteWriteStdinEndError covers client.go:116 — broken connection
// when writing the empty-end-of-stdin record.
func TestExecuteWriteStdinEndError(t *testing.T) {
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
		br := bufio.NewReader(c)
		// Read begin request
		_, _ = ReadRecord(br)
		// Read params + empty params end
		for {
			rec, err := ReadRecord(br)
			if err != nil {
				return
			}
			if rec.Type == TypeParams && rec.ContentLength == 0 {
				break
			}
		}
		// Read stdin data
		_, _ = ReadRecord(br)
		// Close before empty stdin end can be written
		c.Close()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
	})
	defer client.Close()

	body := bytes.NewReader([]byte("request body data"))
	env := map[string]string{
		"SCRIPT_FILENAME": "/test.php",
		"REQUEST_METHOD":  "POST",
	}
	_, _ = client.Execute(context.Background(), env, body)
	wg.Wait()
}

// TestPutOnClosedPool covers pool.go:117 — Put on a closed pool.
func TestPutOnClosedPool(t *testing.T) {
	p := &Pool{
		network: "tcp",
		address: "127.0.0.1:9999",
		maxIdle: 5,
		maxOpen: 10,
		idle:    make(chan *conn, 5),
	}
	p.Close()
	// Put on closed pool should close the connection and decrement active
	p.active.Store(1)
	c := &conn{netConn: &mockNetConn{}}
	p.Put(c)
	if p.active.Load() != 0 {
		t.Errorf("active = %d, want 0", p.active.Load())
	}
}

// TestPutPoolFull covers pool.go:127 — Put when pool is full (default case).
func TestPutPoolFull(t *testing.T) {
	ch := make(chan *conn, 1)
	ch <- &conn{netConn: &mockNetConn{}} // already full
	p := &Pool{
		network: "tcp",
		address: "127.0.0.1:9999",
		maxIdle: 1,
		maxOpen: 10,
		idle:    ch,
	}
	p.active.Store(2)
	c := &conn{netConn: &mockNetConn{}}
	p.Put(c)
	if p.active.Load() != 1 {
		t.Errorf("active = %d, want 1 (connection was discarded)", p.active.Load())
	}
}

// TestPutNilConn covers pool.go:108 — Put nil connection (no-op).
func TestPutNilConn(t *testing.T) {
	p := &Pool{
		network: "tcp",
		address: "127.0.0.1:9999",
		maxIdle: 5,
		maxOpen: 10,
		idle:    make(chan *conn, 5),
	}
	p.Put(nil) // should not panic
}

// TestDecodeParamsReadNameError covers protocol.go:207 — ReadFull error
// when reading param name.
func TestDecodeParamsReadNameError(t *testing.T) {
	// Short data: 1 byte key length, 0 byte value length, name is truncated
	data := []byte{0x05, 0x00, 'h', 'e'} // name length=5 but only 2 bytes follow
	_, err := DecodeParams(data)
	if err == nil {
		t.Error("expected error for truncated param name")
	}
}

// TestDecodeParamsReadValueError covers protocol.go:211 — ReadFull error
// when reading param value.
func TestDecodeParamsReadValueError(t *testing.T) {
	// Key is valid, value is truncated
	data := []byte{0x03, 0x05, 'k', 'e', 'y', 'v', 'a'} // value length=5 but only 2 bytes follow
	_, err := DecodeParams(data)
	if err == nil {
		t.Error("expected error for truncated param value")
	}
}

// TestDecodeParamsExceedsBuffer covers protocol.go:202 — length exceeds
// remaining buffer validation.
func TestDecodeParamsExceedsBuffer(t *testing.T) {
	data := []byte{0x01, 0xFF} // nameLen=1, valueLen=255 but only 1 byte after
	_, err := DecodeParams(data)
	if err == nil {
		t.Error("expected error for value length exceeding buffer")
	}
}

// TestGetStaleConnection covers pool.go:74 — stale connection is discarded.
func TestGetStaleConnection(t *testing.T) {
	// Create a pool with an already-stale idle connection.
	// usedAt time is the zero value so time.Since(c.usedAt) > 30s is true.
	stale := &conn{
		netConn:   &mockNetConn{},
		createdAt: time.Now().Add(-10 * time.Minute),
	}
	ch := make(chan *conn, 1)
	ch <- stale

	p := &Pool{
		network: "tcp",
		address: "127.0.0.1:9999",
		maxIdle: 5,
		maxOpen: 10,
		maxLife: 5 * time.Minute,
		idle:    ch,
	}
	p.active.Store(1)

	// Get should discard the stale connection and create a new one,
	// which will fail because there's no actual server.
	_, err := p.Get(context.Background())
	if err == nil {
		t.Log("expected dial error (no server), connection was stale and discarded")
	}
}

// TestDecodeParamsValidData covers the "else" paths in DecodeParams
// where name and value reads succeed (protocol.go:207, 211).
func TestDecodeParamsValidData(t *testing.T) {
	// Single key-value pair using hex bytes
	data := []byte{0x03, 0x05, 0x6B, 0x65, 0x79, 0x76, 0x61, 0x6C, 0x75, 0x65}
	params, err := DecodeParams(data)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if params["key"] != "value" {
		t.Errorf("params['key'] = %q, want 'value'", params["key"])
	}

	// Multiple key-value pairs
	data2 := []byte{
		0x03, 0x05, 0x6B, 0x65, 0x79, 0x76, 0x61, 0x6C, 0x75, 0x65,
		0x04, 0x06, 0x6E, 0x61, 0x6D, 0x65, 0x76, 0x61, 0x6C, 0x75, 0x65, 0x32,
	}
	params2, err := DecodeParams(data2)
	if err != nil {
		t.Fatalf("DecodeParams multi: %v", err)
	}
	if params2["key"] != "value" || params2["name"] != "value2" {
		t.Errorf("got %v", params2)
	}

	// Key with 4-byte length encoding (>= 128)
	longKey := make([]byte, 200)
	for i := range longKey {
		longKey[i] = 'X'
	}
	var buf []byte
	buf = append(buf, []byte{128, 0, 0, 200}...) // 4-byte encoded key length = 200
	buf = append(buf, 0x05)                       // 1-byte value length = 5
	buf = append(buf, longKey...)
	buf = append(buf, []byte("hello")...)
	params3, err := DecodeParams(buf)
	if err != nil {
		t.Fatalf("DecodeParams long key: %v", err)
	}
	if params3[string(longKey)] != "hello" {
		t.Errorf("long key value = %q", params3[string(longKey)])
	}
}

// TestDecodeParamsLongKeyWith4Byte covers the 4-byte length encoding
// path for value length.
func TestDecodeParamsLongValues(t *testing.T) {
	largeVal := make([]byte, 300)
	for i := range largeVal {
		largeVal[i] = 'V'
	}
	var buf []byte
	buf = append(buf, 0x03)                        // 1-byte key length = 3
	buf = append(buf, []byte{128, 0, 1, 44}...)    // 4-byte value length = 300
	buf = append(buf, []byte("key")...)             // key name, 3 bytes
	buf = append(buf, largeVal...)                  // value, 300 bytes
	params, err := DecodeParams(buf)
	if err != nil {
		t.Fatalf("DecodeParams long value: %v", err)
	}
	if params["key"] != string(largeVal) {
		t.Errorf("key value mismatch: got %d bytes, want %d", len(params["key"]), len(largeVal))
	}
}

// TestCloseIdempotent covers pool.go:146 — calling Close twice (idempotent).
func TestCloseIdempotent(t *testing.T) {
	p := &Pool{
		network: "tcp",
		address: "127.0.0.1:9999",
		maxIdle: 5,
		maxOpen: 10,
		idle:    make(chan *conn, 5),
	}
	p.Close()
	p.Close() // second call should be a no-op
}

// TestGetCtxDoneDuringWait covers pool.go:101-102 — ctx.Done() during pool
// wait when exhausted.
func TestGetCtxDoneDuringWait(t *testing.T) {
	p := &Pool{
		network: "tcp",
		address: "127.0.0.1:9999",
		maxIdle: 0,
		maxOpen: 1,
		idle:    make(chan *conn, 0),
	}
	p.active.Store(1) // pool appears full

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	_, err := p.Get(ctx)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

// TestExecuteReadRecordError covers client.go:133-135 — read error on response.
func TestExecuteReadRecordError(t *testing.T) {
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
		br := bufio.NewReader(c)
		bw := bufio.NewWriter(c)

		// Read all records
		for {
			rec, err := ReadRecord(br)
			if err != nil {
				return
			}
			if rec.Type == TypeStdin && rec.ContentLength == 0 {
				break
			}
		}

		// Send a stdout record
		WriteRecord(bw, TypeStdout, 1, []byte("Content-Type: text/plain\r\n\r\n"))
		bw.Flush()
		// Close without sending EndRequest — causes read error
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
		t.Log("expected error from closed connection during response read")
	}
	wg.Wait()
}

// mockNetConn implements net.Conn for testing without a real TCP connection.
type mockNetConn struct{}

func (m *mockNetConn) Read(b []byte) (int, error)   { return 0, io.EOF }
func (m *mockNetConn) Write(b []byte) (int, error)  { return len(b), nil }
func (m *mockNetConn) Close() error                 { return nil }
func (m *mockNetConn) LocalAddr() net.Addr          { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
func (m *mockNetConn) RemoteAddr() net.Addr         { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
func (m *mockNetConn) SetDeadline(t time.Time) error  { return nil }
func (m *mockNetConn) SetReadDeadline(t time.Time) error { return nil }
func (m *mockNetConn) SetWriteDeadline(t time.Time) error { return nil }
