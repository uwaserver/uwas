package proxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/router"
)

// F40301: the proxy buffers a retryable request body before it picks a
// backend, but forwarded the client's `Expect: 100-continue` anyway. The
// transport's ExpectContinueTimeout is the domain's write timeout (60s by
// default), so an upstream that never answers with 100 Continue (an HTTP/1.0
// or simple backend) received the body only after that timeout, although
// the proxy already held every byte. The expectation is already satisfied
// once the body is buffered, so it is not forwarded.
//
// The fake upstream never sends 100 Continue; it reads the head and then waits
// for the body. The 2s wait is only the detector for a body that never comes,
// far below the transport's 60s.
type expectProbe struct {
	head    string
	bodyGot bool
}

func serveExpect(t *testing.T, contentLength int64, setExpect bool) expectProbe {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan expectProbe, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var sb strings.Builder
		for {
			line, err := br.ReadString('\n')
			sb.WriteString(line)
			if err != nil || line == "\r\n" {
				break
			}
		}
		head := sb.String()
		if strings.Contains(head, "Expect: 100-continue") && contentLength < 0 {
			// Streamed body: answer immediately without reading the body, as a
			// server that rejects the upload early would.
			out <- expectProbe{head: head}
			fmt.Fprint(c, "HTTP/1.1 417 Expectation Failed\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 16)
		n, _ := br.Read(buf)
		out <- expectProbe{head: head, bodyGot: n > 0}
		fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://" + ln.Addr().String(), Weight: 1}})
	req := httptest.NewRequest("POST", "/x", strings.NewReader("hello"))
	if contentLength < 0 {
		req.ContentLength = -1
	}
	if setExpect {
		req.Header.Set("Expect", "100-continue")
	}
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	done := make(chan struct{})
	go func() {
		New(newTestLogger()).Serve(ctx, newTestDomain(), pool, NewBalancer("round_robin"))
		close(done)
	}()
	r := <-out
	<-done
	return r
}

func TestBufferedBodyDoesNotForwardExpectContinue(t *testing.T) {
	// Control: no Expect header, the body arrives straight away.
	if r := serveExpect(t, 5, false); !r.bodyGot {
		t.Fatalf("control: body did not reach the upstream without Expect (head %q)", r.head)
	}

	r := serveExpect(t, 5, true)
	t.Logf("EXPECTED: body delivered without waiting for 100 Continue\nACTUAL: bodyGot=%v head=%q", r.bodyGot, r.head)
	if !r.bodyGot {
		t.Error("PROBLEM CONFIRMED: body withheld while the transport waited for a 100 Continue the upstream never sends")
	}
	if strings.Contains(r.head, "Expect:") {
		t.Errorf("Expect forwarded for a fully buffered body: %q", r.head)
	}
}

// A body that is streamed rather than buffered keeps the expectation, so an
// upstream can still refuse a large upload before the body is sent.
func TestStreamedBodyStillForwardsExpectContinue(t *testing.T) {
	r := serveExpect(t, -1, true)
	if !strings.Contains(r.head, "Expect: 100-continue") {
		t.Errorf("Expect dropped for a streamed body: %q", r.head)
	}
}
