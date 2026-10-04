package proxy

import (
	"errors"
	"fmt"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type activeBodyTransport func(*http.Request) (*http.Response, error)

func (f activeBodyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type activeBodyBody struct {
	entered, release chan struct{}
	closed           bool
	failure          bool
}

func (b *activeBodyBody) Read(p []byte) (int, error) {
	close(b.entered)
	<-b.release
	if b.failure {
		return 0, errors.New("injected body failure")
	}
	return 0, io.EOF
}
func (b *activeBodyBody) Close() error { b.closed = true; return nil }
func activeBodyCase(t *testing.T, buffered, failure bool) int64 {
	t.Helper()
	h := New(logger.New("error", "text"))
	d := &config.Domain{Host: "counter.test"}
	d.Proxy.BufferResponse = buffered
	pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://8.8.8.8"}})
	backend := pool.All()[0]
	body := &activeBodyBody{entered: make(chan struct{}), release: make(chan struct{}), failure: failure}
	control := int64(-1)
	h.getTransport(d)
	h.transports.Range(func(key, value any) bool {
		h.transports.Store(key, activeBodyTransport(func(*http.Request) (*http.Response, error) {
			control = backend.ActiveConns.Load()
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, ContentLength: 0}, nil
		}))
		return true
	})
	ctx := router.AcquireContext(httptest.NewRecorder(), httptest.NewRequest("GET", "http://counter.test/", nil))
	defer router.ReleaseContext(ctx)
	done := make(chan struct{})
	go func() { h.Serve(ctx, d, pool, &RoundRobin{}); close(done) }()
	<-body.entered
	active := backend.ActiveConns.Load()
	close(body.release)
	<-done
	fmt.Printf("CONTROL EXPECTED: 1 ACTUAL: %d; completed EXPECTED: 0 ACTUAL: %d\n", control, backend.ActiveConns.Load())
	if control != 1 || backend.ActiveConns.Load() != 0 || !body.closed {
		t.Fatal("control or cleanup failed")
	}
	return active
}
func TestActiveConnectionsThroughBodyClose(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			if got := activeBodyCase(t, buffered, failure); got != 1 {
				t.Fatalf("active=%d", got)
			}
		}
	}
}
