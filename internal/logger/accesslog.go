package logger

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// AccessLogger writes access logs to a file with buffering.
type AccessLogger struct {
	mu     sync.Mutex
	file   *os.File
	buf    []byte
	format string // "json" or "clf"
}

// NewAccessLogger opens or creates an access log file.
func NewAccessLogger(path, format string) (*AccessLogger, error) {
	if path == "" {
		return nil, nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open access log %s: %w", path, err)
	}

	if format == "" {
		format = "json"
	}

	return &AccessLogger{
		file:   f,
		format: format,
	}, nil
}

// Log writes an access log entry.
func (a *AccessLogger) Log(method, host, path, remoteIP, userAgent, requestID string,
	status int, bytes int64, durationMs, ttfbMs int64) {

	var line string
	now := time.Now()

	switch a.format {
	case "clf":
		// Combined Log Format: ip - - [date] "method path proto" status bytes "referer" "user-agent"
		line = fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" %d %d \"-\" \"%s\"\n",
			remoteIP,
			now.Format("02/Jan/2006:15:04:05 -0700"),
			method, path, status, bytes, userAgent)
	default:
		// JSON format
		line = fmt.Sprintf(`{"time":"%s","method":"%s","host":"%s","path":"%s","status":%d,"bytes":%d,"duration_ms":%d,"ttfb_ms":%d,"remote":"%s","user_agent":"%s","request_id":"%s"}`+"\n",
			now.Format(time.RFC3339), method, host, path, status, bytes,
			durationMs, ttfbMs, remoteIP, userAgent, requestID)
	}

	a.mu.Lock()
	a.file.WriteString(line)
	a.mu.Unlock()
}

// Close flushes and closes the log file.
func (a *AccessLogger) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	return a.file.Close()
}
