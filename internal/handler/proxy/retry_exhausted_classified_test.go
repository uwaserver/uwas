package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/testport"
)

// deadUpstream returns the address of a listener that was just closed, so a
// dial to it is refused.
func deadUpstream(t *testing.T) string {
	t.Helper()
	return testport.DeadURL(t)
}

// TestRetriesExhaustedKeepsUpstreamClassification pins that when every backend
// is tried and fails, the 502 body still carries the classified cause
// (classifyUpstreamErr) instead of the generic "all backends failed", which
// told the operator nothing (F19800). With maxRetries clamped to
// len(backends) the final loop pass found no untried backend and broke out.
func TestRetriesExhaustedKeepsUpstreamClassification(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4} {
		var ups []UpstreamConfig
		for i := 0; i < n; i++ {
			ups = append(ups, UpstreamConfig{Address: deadUpstream(t), Weight: 1})
		}
		front := serveThroughProxy(t, NewUpstreamPool(ups))
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("%d dead backends: status %d, want 502", n, resp.StatusCode)
		}
		if !strings.Contains(string(body), "refused connection") {
			t.Errorf("%d dead backends: body %q, want the classified cause (upstream refused connection)", n, strings.TrimSpace(string(body)))
		}
	}
}

// TestRetryStillReachesLiveBackendAfterClamp is the edge-case guard for the
// F19800 fix: a dead backend followed by a live one is still retried, and a
// lone live backend is served.
func TestRetryStillReachesLiveBackendAfterClamp(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer live.Close()
	for _, ups := range [][]UpstreamConfig{
		{{Address: live.URL, Weight: 1}},
		{{Address: deadUpstream(t), Weight: 1}, {Address: live.URL, Weight: 1}},
	} {
		front := serveThroughProxy(t, NewUpstreamPool(ups))
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "ok" {
			t.Errorf("%d backends: got %d %q, want 200 ok", len(ups), resp.StatusCode, body)
		}
	}
}
