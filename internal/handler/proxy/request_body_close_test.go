package proxy

import (
	"errors"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bufferedInputBody struct {
	io.Reader
	closes int
}

func (b *bufferedInputBody) Close() error { b.closes++; return nil }

type bufferedInputFailure struct{ partial bool }

func (b *bufferedInputFailure) Read(p []byte) (int, error) {
	if b.partial {
		b.partial = false
		copy(p, "part")
		return 4, errors.New("injected read failure")
	}
	return 0, errors.New("injected read failure")
}

type bufferedInputTransport struct{}

func (bufferedInputTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
func bufferedInputCase(t *testing.T, r io.Reader, status int) int {
	t.Helper()
	h := New(testLogger())
	d := &config.Domain{Host: "body.test"}
	h.getTransport(d)
	h.transports.Range(func(k, v any) bool { h.transports.Store(k, bufferedInputTransport{}); return true })
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://8.8.8.8"}})
	body := &bufferedInputBody{Reader: r}
	req := httptest.NewRequest("POST", "http://body.test/", nil)
	req.Body = body
	req.ContentLength = 1
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	h.Serve(ctx, d, pool, &RoundRobin{})
	if rec.Code != status {
		t.Fatalf("status=%d want=%d", rec.Code, status)
	}
	return body.closes
}
func TestBufferedRequestBodyAlwaysCloses(t *testing.T) {
	for _, r := range []io.Reader{strings.NewReader(""), strings.NewReader("ok")} {
		if got := bufferedInputCase(t, r, 200); got != 1 {
			t.Fatal(got)
		}
	}
	for _, partial := range []bool{false, true} {
		if got := bufferedInputCase(t, &bufferedInputFailure{partial: partial}, 502); got != 1 {
			t.Fatal(got)
		}
	}
	if got := bufferedInputCase(t, strings.NewReader(strings.Repeat("a", int(maxRetryBodyBytes)+1)), 413); got != 1 {
		t.Fatal(got)
	}
}
