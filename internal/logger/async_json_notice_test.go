package logger

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// noticeWriter blocks writes until released and signals the drop notice.
type noticeWriter struct {
	release chan struct{}
	seen    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	notice  string
}

func (n *noticeWriter) Write(p []byte) (int, error) {
	<-n.release
	if strings.Contains(string(p), "backlogged") {
		n.mu.Lock()
		n.notice = strings.TrimSpace(string(p))
		n.mu.Unlock()
		n.once.Do(func() { close(n.seen) })
	}
	return len(p), nil
}

func dropNotice(t *testing.T, jsonMode bool) string {
	t.Helper()
	w := &noticeWriter{release: make(chan struct{}), seen: make(chan struct{})}
	a := newAsyncWriterFormat(w, jsonMode)
	for i := 0; i < asyncQueueLen*2; i++ {
		_, _ = a.Write([]byte("line\n"))
	}
	close(w.release)
	<-w.seen
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.notice
}

// A JSON log stream must stay JSON when lines are dropped: the notice used to
// be a logfmt line, which broke every parser reading the stream.
func TestAsyncWriterDropNoticeIsJSONInJSONMode(t *testing.T) {
	got := dropNotice(t, true)
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("drop notice is not JSON: %q (%v)", got, err)
	}
	if m["level"] != "WARN" || m["msg"] != "log output backlogged, lines dropped" {
		t.Fatalf("unexpected notice fields: %v", m)
	}
	if n, _ := m["dropped"].(float64); n < 1 {
		t.Fatalf("dropped count missing: %v", m)
	}
	if _, ok := m["time"].(string); !ok {
		t.Fatalf("time missing: %v", m)
	}
}

func TestAsyncWriterDropNoticeStaysLogfmtInTextMode(t *testing.T) {
	got := dropNotice(t, false)
	if !strings.HasPrefix(got, "time=") || !strings.Contains(got, "level=WARN") || !strings.Contains(got, "dropped=") {
		t.Fatalf("text-mode notice changed: %q", got)
	}
}
