package deploy

import (
	"strings"
	"sync"
)

// LogSink buffers deploy command output. *strings.Builder and *LiveLog both
// implement it so callers can opt into live streaming without forking the
// git pipeline.
type LogSink interface {
	WriteString(s string) (int, error)
	Write(p []byte) (int, error)
	String() string
}

// LiveLog is a LogSink that fans each write out to Emit (for SSE / live UI)
// while keeping a full buffer for the final deploy response.
type LiveLog struct {
	mu   sync.Mutex
	buf  strings.Builder
	Emit func(chunk string)
}

func (l *LiveLog) WriteString(s string) (int, error) {
	if l == nil {
		return 0, nil
	}
	l.mu.Lock()
	n, _ := l.buf.WriteString(s)
	emit := l.Emit
	l.mu.Unlock()
	if emit != nil && s != "" {
		emit(s)
	}
	return n, nil
}

func (l *LiveLog) Write(p []byte) (int, error) {
	return l.WriteString(string(p))
}

func (l *LiveLog) String() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// Ensure *strings.Builder continues to satisfy LogSink.
var _ LogSink = (*strings.Builder)(nil)
var _ LogSink = (*LiveLog)(nil)
