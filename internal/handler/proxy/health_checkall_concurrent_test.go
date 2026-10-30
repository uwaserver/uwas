package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// concurrentHealthTransport blocks each probe until release is closed (or the
// request is cancelled) and counts how many probes are in flight at once.
type concurrentHealthTransport struct {
	inflight *atomic.Int32
	arrived  chan struct{}
	release  chan struct{}
}

func (rt concurrentHealthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.inflight.Add(1)
	rt.arrived <- struct{}{}
	select {
	case <-rt.release:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
}

// F58000: checkAll probed backends one after another, so N unresponsive
// backends delayed detection of every later backend by N × timeout. All
// probes must be in flight together.
func TestCheckAllProbesBackendsConcurrently(t *testing.T) {
	const n = 4
	cfgs := make([]UpstreamConfig, n)
	for i := range cfgs {
		cfgs[i] = UpstreamConfig{Address: "http://8.8.8." + string(rune('1'+i))}
	}
	pool := NewUpstreamPool(cfgs)
	hc := NewHealthChecker(pool, HealthConfig{Timeout: time.Minute, Rise: 1}, logger.New("error", "text"))
	rt := concurrentHealthTransport{&atomic.Int32{}, make(chan struct{}, n), make(chan struct{})}
	hc.client.Transport = rt

	done := make(chan struct{})
	go func() { hc.checkAll(); close(done) }()

	for i := 0; i < n; i++ {
		select {
		case <-rt.arrived:
		case <-time.After(5 * time.Second):
			close(rt.release)
			<-done
			t.Fatalf("only %d of %d probes in flight together: checkAll is sequential", i, n)
		}
	}
	close(rt.release)
	<-done
	for _, b := range pool.All() {
		if !b.IsHealthy() {
			t.Fatalf("backend %s not healthy after successful probe", b.URL)
		}
	}
}
