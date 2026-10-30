package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// streamingServer answers every request with up to total bytes of body and
// counts what it managed to write before the client went away.
func streamingServer(t *testing.T, status int, total int64) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
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
	return srv, &sent
}

// The webhook and Slack channels read at most 4 KiB of the reply; Telegram read
// all of it, so a misbehaving endpoint made every alert pull as much as the
// 10 s timeout allowed.
func TestTelegramReadsBoundedResponse(t *testing.T) {
	const total = 128 << 20
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		srv, sent := streamingServer(t, status, total)
		oldBase := telegramAPIBase
		telegramAPIBase = srv.URL
		resetServerIdentityForTest()
		err := Send(Channel{Type: "telegram", Enabled: true, Config: map[string]string{"bot_token": "1:T", "chat_id": "1"}}, Message{Title: "t"})
		telegramAPIBase = oldBase
		got := sent.Load()
		t.Logf("EXPECTED: a few KiB read\nACTUAL: status %d err=%v, %d bytes written by the server", status, err, got)
		if status == http.StatusInternalServerError && (err == nil || !strings.Contains(err.Error(), "500")) {
			t.Errorf("a 500 reply must still be reported: %v", err)
		}
		if got > 16<<20 {
			t.Fatalf("PROBLEM CONFIRMED: status %d — the Telegram channel pulled %d bytes of the reply", status, got)
		}
	}
}

// Control: the other channels already stop reading; same bound.
func TestWebhookChannelReadsBoundedResponse(t *testing.T) {
	srv, sent := streamingServer(t, http.StatusOK, 128<<20)
	err := Send(Channel{Type: "webhook", Enabled: true, Config: map[string]string{"url": srv.URL + "/h"}}, Message{Title: "t"})
	_ = err
	if got := sent.Load(); got > 16<<20 {
		t.Fatalf("the webhook channel pulled %d bytes of the reply", got)
	}
}
