package fastcgi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// --- protocol.go: WriteRecord overflow guard (protocol.go:96-98) ---

func TestWriteRecordOverflowGuard(t *testing.T) {
	var buf bytes.Buffer
	content := make([]byte, maxContentLength+1)
	err := WriteRecord(&buf, TypeStdout, 1, content)
	if err == nil {
		t.Fatal("expected error for oversized record content")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Errorf("error = %v, want overflow message", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should be written on overflow, wrote %d bytes", buf.Len())
	}
}

// TestWriteRecordExactMax confirms the boundary (exactly maxContentLength) is allowed.
func TestWriteRecordExactMax(t *testing.T) {
	var buf bytes.Buffer
	content := make([]byte, maxContentLength)
	if err := WriteRecord(&buf, TypeStdout, 1, content); err != nil {
		t.Fatalf("WriteRecord at max length: %v", err)
	}
	// header(8) + content(65535) + padding(1 -> to 8 boundary: 65535%8=7 -> pad 1)
	rec, err := ReadRecord(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if int(rec.ContentLength) != maxContentLength {
		t.Errorf("ContentLength = %d, want %d", rec.ContentLength, maxContentLength)
	}
}

// --- client.go: PARAMS chunking for >64KB env (client.go:76-88) ---

func TestExecuteParamsChunkingOver64KB(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var paramBytes int
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
		for {
			rec, err := ReadRecord(br)
			if err != nil {
				return
			}
			if rec.Type == TypeParams {
				mu.Lock()
				paramBytes += len(rec.Content)
				mu.Unlock()
			}
			if rec.Type == TypeStdin && rec.ContentLength == 0 {
				break
			}
		}
		WriteRecord(bw, TypeStdout, 1, []byte("Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nok"))
		WriteRecord(bw, TypeStdout, 1, nil)
		end := make([]byte, 8)
		WriteRecord(bw, TypeEndRequest, 1, end)
		bw.Flush()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	defer client.Close()

	// Build an env whose encoded form exceeds maxContentLength (64KB),
	// forcing the chunking loop to split it across multiple PARAMS records.
	env := map[string]string{"REQUEST_METHOD": "GET", "SCRIPT_FILENAME": "/x.php"}
	bigVal := strings.Repeat("A", 4000)
	for i := 0; i < 40; i++ {
		env["HTTP_X_HEADER_"+strings.Repeat("N", 0)+string(rune('a'+i))] = bigVal
	}
	encoded := EncodeParams(env)
	if len(encoded) <= maxContentLength {
		t.Fatalf("test setup: encoded params %d must exceed %d", len(encoded), maxContentLength)
	}

	resp, err := client.Execute(context.Background(), env, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wg.Wait()

	mu.Lock()
	got := paramBytes
	mu.Unlock()
	// Server reassembles all PARAMS chunks; total must equal the encoded length.
	if got != len(encoded) {
		t.Errorf("server received %d param bytes, want %d", got, len(encoded))
	}
	if !strings.Contains(string(resp.Stdout()), "ok") {
		t.Errorf("stdout = %q", resp.Stdout())
	}
}

// --- client.go: Location header upgrades 200 -> 302 (client.go:203-205) ---

func TestParseHTTPLocationUpgradesTo302(t *testing.T) {
	r := &Response{}
	r.stdout.WriteString("Content-Type: text/html\r\nLocation: /login\r\n\r\n")

	status, headers, _ := r.ParseHTTP()
	if status != 302 {
		t.Errorf("status = %d, want 302 (Location without Status)", status)
	}
	if headers.Get("Location") != "/login" {
		t.Errorf("Location = %q", headers.Get("Location"))
	}
}

// TestParseHTTPLocationWithExplicitStatusNotUpgraded ensures an explicit
// Status header is respected (Location does not override it).
func TestParseHTTPLocationWithExplicitStatusNotUpgraded(t *testing.T) {
	r := &Response{}
	r.stdout.WriteString("Status: 201 Created\r\nLocation: /resource/1\r\n\r\n")

	status, _, _ := r.ParseHTTP()
	if status != 201 {
		t.Errorf("status = %d, want 201 (explicit Status preserved)", status)
	}
}

// --- client.go: write error during PARAMS chunking on a dead connection
// (client.go:76-88, broken=true path). The >64KB params force bufio flushes
// mid-stream so the write surfaces the error at WriteRecord, not just Flush. ---

func TestExecuteParamsChunkWriteErrorOnDeadConn(t *testing.T) {
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
		// Read only the begin request, then slam the connection shut so the
		// subsequent large PARAMS writes (which flush mid-stream) fail.
		hdr := make([]byte, 16)
		io.ReadFull(c, hdr)
		c.Close()
	}()

	client := NewClient(PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	defer client.Close()

	env := map[string]string{"REQUEST_METHOD": "GET"}
	bigVal := strings.Repeat("Q", 8000)
	for i := 0; i < 60; i++ {
		env["HTTP_FILLER_"+string(rune('a'+i%26))+string(rune('A'+i))] = bigVal
	}
	encoded := EncodeParams(env)
	if len(encoded) <= maxContentLength {
		t.Fatalf("setup: encoded %d must exceed %d", len(encoded), maxContentLength)
	}

	// We don't assert a specific error string — the connection death may be
	// observed at any write/flush stage. The point is exercising the broken
	// path without panicking.
	_, _ = client.Execute(context.Background(), env, nil)
	wg.Wait()
}

var _ = binary.BigEndian
