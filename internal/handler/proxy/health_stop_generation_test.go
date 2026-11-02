package proxy

import (
	"context"
	"github.com/uwaserver/uwas/internal/logger"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type stoppedProbeTransport struct {
	entered, release chan struct{}
	status           int
}

func (rt stoppedProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if rt.entered != nil {
		close(rt.entered)
		<-rt.release
	}
	return &http.Response{StatusCode: rt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
func TestStoppedHealthProbeCannotPublish(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, status := range []int{200, 500} {
			t.Run(map[bool]string{false: "stop", true: "restart"}[restart]+http.StatusText(status), func(t *testing.T) {
				pool := NewUpstreamPool([]UpstreamConfig{{Address: "http://8.8.8.8"}})
				b := pool.All()[0]
				hc := NewHealthChecker(pool, HealthConfig{Rise: 1, Threshold: 1, Interval: time.Hour}, logger.New("error", "text"))
				defer hc.Stop()
				initial := StateUnhealthy
				if status == 500 {
					initial = StateHealthy
				}
				b.SetState(initial)
				hc.client.Transport = stoppedProbeTransport{status: status}
				hc.checkOne(b)
				if b.GetState() == initial {
					t.Fatal("active control did not publish")
				}
				b.SetState(initial)
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				hc.client.Transport = stoppedProbeTransport{entered, release, status}
				go func() { hc.checkAll(); close(done) }()
				<-entered
				hc.Stop()
				hc.Stop()
				if restart {
					hc.Start(context.Background())
				}
				close(release)
				<-done
				if b.GetState() != initial || len(hc.successes) != 0 || len(hc.failures) != 0 {
					t.Fatalf("stale publication: state=%v successes=%v failures=%v", b.GetState(), hc.successes, hc.failures)
				}
				hc.client.Transport = stoppedProbeTransport{status: status}
				hc.checkOne(b)
				if b.GetState() == initial {
					t.Fatal("new generation did not publish")
				}
			})
		}
	}
}
