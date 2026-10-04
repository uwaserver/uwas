package proxy

import (
	"github.com/uwaserver/uwas/internal/logger"
	"io"
	"net/http"
	"strings"
	"testing"
)

type drainingHealthTransport struct{ entered, release chan struct{} }

func (rt drainingHealthTransport) RoundTrip(*http.Request) (*http.Response, error) {
	close(rt.entered)
	<-rt.release
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
}

func TestHealthSuccessPreservesDraining(t *testing.T) {
	for _, state := range []BackendState{StateUnhealthy, StateHealthy, StateDraining} {
		t.Run(map[BackendState]string{StateUnhealthy: "recover", StateHealthy: "healthy", StateDraining: "draining"}[state], func(t *testing.T) {
			pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://8.8.8.8"}})
			b := pool.All()[0]
			b.SetState(StateUnhealthy)
			hc := NewHealthChecker(pool, HealthConfig{Rise: 1}, logger.New("error", "text"))
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			hc.client.Transport = drainingHealthTransport{entered, release}
			go func() { hc.checkOne(b); close(done) }()
			<-entered
			b.SetState(state)
			close(release)
			<-done
			want := StateHealthy
			if state == StateDraining {
				want = StateDraining
			}
			if got := b.GetState(); got != want {
				t.Fatalf("state after probe = %v, want %v", got, want)
			}
		})
	}
}
