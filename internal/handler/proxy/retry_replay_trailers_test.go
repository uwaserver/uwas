package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// firstBackend always selects backends[0] so the retry target is fixed.
type firstBackend struct{}

func (firstBackend) Select(b []*Backend, _ *http.Request) *Backend {
	if len(b) == 0 {
		return nil
	}
	return b[0]
}

func serveThroughProxy(t *testing.T, pool *UpstreamPool) *httptest.Server {
	t.Helper()
	h := New(newTestLogger())
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Serve(router.AcquireContext(w, r), newTestDomain(), pool, firstBackend{})
	}))
	t.Cleanup(front.Close)
	return front
}

// TestNonIdempotentRequestNotReplayedAfterSend pins that a POST the first
// upstream already received is not replayed to another backend when that
// upstream resets the connection, while an idempotent GET still is.
func TestNonIdempotentRequestNotReplayedAfterSend(t *testing.T) {
	for _, tc := range []struct {
		method     string
		wantStatus int
		wantTotal  int
	}{
		{http.MethodPost, http.StatusBadGateway, 1},
		{http.MethodGet, http.StatusOK, 2},
	} {
		var mu sync.Mutex
		hits := 0
		count := func(r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			hits++
			mu.Unlock()
		}
		a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count(r)
			c, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			if tc, ok := c.(*net.TCPConn); ok {
				_ = tc.SetLinger(0) // RST: request delivered, no response
			}
			_ = c.Close()
		}))
		b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count(r)
			_, _ = w.Write([]byte("ok"))
		}))
		pool := NewUpstreamPool([]UpstreamConfig{{Address: a.URL, Weight: 1}, {Address: b.URL, Weight: 1}})
		front := serveThroughProxy(t, pool)

		req, _ := http.NewRequest(tc.method, front.URL+"/order", strings.NewReader("amount=100"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.method, err)
		}
		resp.Body.Close()
		a.Close()
		b.Close()
		mu.Lock()
		got := hits
		mu.Unlock()
		if resp.StatusCode != tc.wantStatus || got != tc.wantTotal {
			t.Errorf("%s: status=%d upstream deliveries=%d, want status=%d deliveries=%d",
				tc.method, resp.StatusCode, got, tc.wantStatus, tc.wantTotal)
		}
	}
}

// TestUpstreamTrailersForwarded pins that response trailers (the carrier of
// gRPC's grpc-status) reach the client.
func TestUpstreamTrailersForwarded(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "Grpc-Status")
		_, _ = w.Write([]byte("payload"))
		w.Header().Set("Grpc-Status", "0")
	}))
	defer up.Close()
	front := serveThroughProxy(t, NewUpstreamPool([]UpstreamConfig{{Address: up.URL, Weight: 1}}))

	resp, err := http.Get(front.URL + "/svc")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "payload" || resp.Trailer.Get("Grpc-Status") != "0" {
		t.Fatalf("body=%q trailers=%v, want payload and Grpc-Status=0", body, resp.Trailer)
	}
}
