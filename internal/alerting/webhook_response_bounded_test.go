package alerting

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// The webhook only needs the status code. A receiver that answers with a huge
// (or endless) body made every delivery read as much as the 10 s client
// timeout allowed.
func TestSendWebhookReadsBoundedResponse(t *testing.T) {
	const total = 128 << 20
	var sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := []byte(strings.Repeat("x", 64<<10))
		fl, _ := w.(http.Flusher)
		for sent.Load() < total {
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	a := New(true, srv.URL, nil, logger.New("error", "text"))
	a.urlSafetyCheck = nil
	a.dialControl = nil
	if err := a.sendWebhook(Alert{Level: "warning", Type: "t", Host: "h", Message: "m"}); err != nil {
		t.Fatalf("a 200 delivery failed: %v", err)
	}
	got := sent.Load()
	t.Logf("EXPECTED: a few KiB read\nACTUAL: %d bytes written by the receiver", got)
	if got > 16<<20 {
		t.Fatalf("PROBLEM CONFIRMED: the alert webhook pulled %d bytes of the reply", got)
	}
}
