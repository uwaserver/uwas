package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// "TE" is hop-by-hop, but "TE: trailers" is the one value a proxy passes on so
// the backend knows trailers may be sent (httputil.ReverseProxy does the same,
// Go issue 21096); gRPC clients send it on every call. Dropping it with the
// rest of the TE field hid trailer support from the backend (F25500).
func serveWithTE(t *testing.T, te string) string {
	t.Helper()
	addr, got := rawUpstreamHead(t, "")
	pool := NewUpstreamPool([]UpstreamConfig{{Address: addr, Weight: 1}})
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("X-Control", "keep")
	if te != "" {
		req.Header.Set("Te", te)
	}
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	New(newTestLogger()).Serve(ctx, newTestDomain(), pool, NewBalancer("round_robin"))
	head := <-got
	if !strings.Contains(head, "X-Control: keep") || rec.Code != 200 {
		t.Fatalf("invalid proof: control failed (code %d)\n%s", rec.Code, head)
	}
	return head
}

func TestProxyForwardsTETrailers(t *testing.T) {
	for _, te := range []string{"trailers", "Trailers", "gzip, trailers"} {
		head := serveWithTE(t, te)
		if !strings.Contains(head, "Te: trailers\r\n") {
			t.Errorf("TE %q: upstream did not receive \"Te: trailers\":\n%s", te, head)
		}
		if strings.Contains(strings.ToLower(head), "te: gzip") {
			t.Errorf("TE %q: non-trailers coding leaked upstream:\n%s", te, head)
		}
	}
}

func TestProxyDropsOtherTE(t *testing.T) {
	for _, te := range []string{"", "gzip", "deflate;q=0.5"} {
		head := serveWithTE(t, te)
		if strings.Contains(strings.ToLower(head), "\r\nte:") {
			t.Errorf("TE %q: hop-by-hop TE forwarded:\n%s", te, head)
		}
	}
}
