package cache

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// countingReader serves up to n bytes of 'a' with NO newline, counting how
// much was actually drained by the parser.
type countingReader struct {
	remaining int64
	consumed  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > c.remaining {
		n = int(c.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	c.remaining -= int64(n)
	c.consumed += int64(n)
	return n, nil
}

// TestReadLineNoNewlineIsBounded is a regression test for unbounded line
// buffering.
//
// readLine used bufio.Reader.ReadBytes('\n'), which accumulates everything
// up to EOF when no newline arrives. readReply calls readLine first for
// EVERY RESP reply, so a peer streaming bytes with no newline grew memory
// without bound on the request path (L3 Get on cache miss, Keys on purge) —
// same reachability as the hostile length headers: a misconfigured address,
// a compromised Redis, or a plaintext MITM (TLS is optional). readLine now
// rejects lines longer than maxLineLen (64 KiB), built on ReadSlice so
// bytes after a newline remain buffered for the next read.
func TestReadLineNoNewlineIsBounded(t *testing.T) {
	cr := &countingReader{remaining: 4 << 20} // 4 MiB, no newline
	br := bufio.NewReader(cr)
	_, _ = readLine(br) // error expected either way (EOF / line too long)

	if cr.consumed > 1<<20 {
		t.Fatalf("readLine drained %d bytes of a 4 MiB unterminated stream into memory (bound 1 MiB) — unbounded line buffering lets a hostile peer exhaust server memory", cr.consumed)
	}
}

// TestReadLineWellFormedControl is the control: well-formed replies keep
// parsing through the full readReply path, and a multi-element array proves
// bytes after '\n' remain buffered for the next read (the property ReadBytes
// provided and the ReadSlice rewrite must keep).
func TestReadLineWellFormedControl(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("$5\r\nhello\r\n"))
	v, err := readReply(r)
	if err != nil || v != "hello" {
		t.Fatalf("control: got %v, %v; want hello, nil", v, err)
	}

	r2 := bufio.NewReader(strings.NewReader("*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n"))
	v2, err2 := readReply(r2)
	if err2 != nil {
		t.Fatalf("control: array parse error %v", err2)
	}
	arr, ok := v2.([]any)
	if !ok || len(arr) != 2 || arr[0] != "foo" || arr[1] != "bar" {
		t.Fatalf("control: array = %v", v2)
	}
}

// TestReadLineMalformedControl: a line missing its CR is still rejected as
// malformed.
func TestReadLineMalformedControl(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("+OK\n"))
	if _, err := readLine(r); err == nil {
		t.Fatal("control: LF without CR must be rejected")
	}
}
