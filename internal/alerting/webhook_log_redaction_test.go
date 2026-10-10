package alerting

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

type sigBuf struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
	want int
	n    int
}

func (s *sigBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.Write(p)
	if strings.Contains(string(p), "webhook delivery failed") {
		s.n++
		if s.n == s.want {
			close(s.done)
		}
	}
	return len(p), nil
}

// run delivers one alert to a dead receiver whose URL carries a credential
// and returns everything the alerter logged.
func runDeadWebhook(t *testing.T, path string) string {
	srv := httptest.NewServer(nil)
	base := srv.URL
	srv.Close() // connection refused
	sb := &sigBuf{done: make(chan struct{}), want: 2}
	lg := &logger.Logger{Logger: slog.New(slog.NewTextHandler(sb, nil))}
	a := New(true, base+path, nil, lg)
	a.urlSafetyCheck, a.dialControl = nil, nil
	a.Alert(Alert{Level: "warning", Type: "t", Host: "h", Message: "m"})
	<-sb.done // sendWebhook's own log + Alert goroutine's log
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

// F941: the receiver URL is a credential; delivery-failure logs and returned
// errors must keep the cause but not the URL.
func TestWebhookFailureLogsOmitReceiverURL(t *testing.T) {
	const secret = "T0K3N-s3cr3t"
	// dead receiver: logs and returned error carry the cause, not the URL
	out := runDeadWebhook(t, "/services/"+secret)
	if strings.Contains(out, secret) || !strings.Contains(out, "connection refused") {
		t.Fatalf("dead receiver log:\n%s", out)
	}
	// HTTP 500: status logged, URL not
	var buf bytes.Buffer
	lg := &logger.Logger{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	a := New(true, srv.URL+"/services/"+secret, nil, lg)
	a.urlSafetyCheck, a.dialControl = nil, nil
	err := a.sendWebhook(Alert{Type: "t"})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(buf.String(), secret) || !strings.Contains(buf.String(), "status=500") {
		t.Fatalf("500 case err=%v log=%s", err, buf.String())
	}
	// redirect refused by the SSRF check: neither the returned error nor the log leaks the original URL
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/x", http.StatusFound)
	}))
	defer redir.Close()
	buf.Reset()
	b := New(true, redir.URL+"/services/"+secret, nil, lg)
	b.dialControl = nil
	b.urlSafetyCheck = func(u string) error {
		if strings.HasPrefix(u, redir.URL) {
			return nil
		}
		return errors.New("blocked (SSRF)")
	}
	err = b.sendWebhook(Alert{Type: "t"})
	if err == nil || !strings.Contains(err.Error(), "SSRF") || strings.Contains(err.Error(), secret) || strings.Contains(buf.String(), secret) {
		t.Fatalf("redirect case err=%v log=%s", err, buf.String())
	}
	// success path stays silent
	buf.Reset()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	c := New(true, ok.URL+"/services/"+secret, nil, lg)
	c.urlSafetyCheck, c.dialControl = nil, nil
	if err := c.sendWebhook(Alert{Type: "t"}); err != nil || buf.Len() != 0 {
		t.Fatalf("success path err=%v log=%s", err, buf.String())
	}

}
