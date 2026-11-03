package proxy

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

type healthBodyBoundRT struct{ remaining int }

func (rt healthBodyBoundRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       &healthBodyThenError{remaining: rt.remaining},
		Request:    req,
	}, nil
}

type healthBodyThenError struct{ remaining int }

func (b *healthBodyThenError) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, errors.New("stream continues beyond probe budget")
	}
	n := min(len(p), b.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	b.remaining -= n
	return n, nil
}

func (*healthBodyThenError) Close() error { return nil }

func TestHealthProbeDoesNotRequireResponseBodyEOF(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bodyBytes int
		healthy   bool
	}{
		{name: "stream continues after budget", bodyBytes: 64 << 10, healthy: true},
		{name: "body breaks inside budget", bodyBytes: (64 << 10) - 1, healthy: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://127.0.0.1:8080", Weight: 1}})
			hc := NewHealthChecker(pool, HealthConfig{Threshold: 1, Rise: 1}, logger.New("error", "text"))
			hc.client.Transport = healthBodyBoundRT{remaining: tc.bodyBytes}
			backend := pool.All()[0]

			hc.checkOne(backend)

			if got := backend.IsHealthy(); got != tc.healthy {
				t.Fatalf("backend healthy = %v, want %v", got, tc.healthy)
			}
		})
	}
}

var _ io.ReadCloser = (*healthBodyThenError)(nil)
