package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/iotest"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	proxyhandler "github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/logger"
)

// F28200: a request that Allow() admitted as the half-open probe but that
// returned before its outcome was recorded (here: the client's body broke while
// the mirror buffered it) left the probe slot claimed. Every later Allow()
// lost the slot CAS, so the domain answered 503 forever even though the
// upstream was healthy.
func TestHandleProxyAbandonedProbeReleasesCircuit(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer primary.Close()
	shadow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer shadow.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host: "cb.test", Type: "proxy", SSL: config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams:             []config.Upstream{{Address: primary.URL}},
				AllowPrivateUpstreams: true,
				Mirror:                config.MirrorConfig{Enabled: true, Backend: shadow.URL, Percent: 100, MaxBodyBytes: 1024},
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.rebuildProxyPools(cfg.Domains)

	cb := proxyhandler.NewCircuitBreaker(1, time.Nanosecond)
	cb.RecordFailure() // open; the timeout has already elapsed
	s.routeMu.Lock()
	s.proxyBreakers["cb.test"] = cb
	s.routeMu.Unlock()

	// The probe: the body fails mid-read.
	req := httptest.NewRequest("POST", "/x", iotest.ErrReader(http.ErrBodyReadAfterClose))
	req.ContentLength = 5
	req.Host = "cb.test"
	rec := httptest.NewRecorder()
	s.handleRequest(rec, req)
	t.Logf("EXPECTED: probe slot released, next request admitted\nACTUAL: first status=%d", rec.Code)

	// Control: a healthy request afterwards must be served by the primary.
	req2 := httptest.NewRequest("GET", "/x", nil)
	req2.Host = "cb.test"
	rec2 := httptest.NewRecorder()
	s.handleRequest(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("PROBLEM CONFIRMED: breaker wedged half-open after abandoned probe: next status=%d, want 200", rec2.Code)
	}
}
