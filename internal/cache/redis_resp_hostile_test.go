package cache

import (
	"bufio"
	"strings"
	"testing"
)

// mustNotPanic converts a panic inside fn into an explicit test failure so
// a regression of the length-cap guards reads as FAIL rather than a raw
// crash.
func mustNotPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("%s panicked: %v — a hostile Redis length header crashes the RESP parser on the request path (L3 Get on cache miss, Keys on purge)", name, rec)
		}
	}()
	fn()
}

// TestReadReplyHostileBulkLength is a regression test for the $ branch
// trusting the wire length header in make() without a bound:
//
//	$9223372036854775807\r\n  →  n = MaxInt64  →  make([]byte, n+2)
//	n+2 overflows to negative  →  panic: makeslice: len out of range
//
// A wire parser must be robust to arbitrary bytes: the Redis peer can be a
// misconfigured address, a compromised server, or a plaintext MITM (TLS is
// optional). readReply now rejects lengths above maxBulkLen.
func TestReadReplyHostileBulkLength(t *testing.T) {
	mustNotPanic(t, "bulk length overflow", func() {
		r := bufio.NewReader(strings.NewReader("$9223372036854775807\r\n"))
		if _, err := readReply(r); err == nil {
			t.Fatal("MaxInt64 bulk length must be rejected with an error, not accepted")
		}
	})
}

// TestReadReplyHostileArrayLength is a regression test for the * branch
// with the same defect: *9223372036854775807 makes make([]any, MaxInt64)
// exceed maxAlloc — panic: makeslice: len out of range. readReply now
// rejects lengths above maxArrayLen.
func TestReadReplyHostileArrayLength(t *testing.T) {
	mustNotPanic(t, "array length overflow", func() {
		r := bufio.NewReader(strings.NewReader("*9223372036854775807\r\n"))
		if _, err := readReply(r); err == nil {
			t.Fatal("MaxInt64 array length must be rejected with an error, not accepted")
		}
	})
}

// TestReadReplyWellFormedControl is the control: ordinary replies keep
// parsing, so the hostile-length guards are specific to the missing bound.
func TestReadReplyWellFormedControl(t *testing.T) {
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
