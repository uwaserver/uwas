package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// domainLogManager manages per-domain access log files.
type domainLogManager struct {
	mu    sync.RWMutex
	files map[string]*domainLogFile
}

type domainLogFile struct {
	f       *os.File
	path    string
	written int64
}

func newDomainLogManager() *domainLogManager {
	return &domainLogManager{files: make(map[string]*domainLogFile)}
}

// Write writes an access log entry for the given domain.
// If the domain has no configured log path, this is a no-op.
func (m *domainLogManager) Write(host, logPath string, method, path, remoteIP, userAgent string, status, bytes int, duration time.Duration) {
	if logPath == "" {
		return
	}

	m.mu.RLock()
	dlf, ok := m.files[host]
	m.mu.RUnlock()

	if !ok {
		m.mu.Lock()
		// Double-check after lock
		dlf, ok = m.files[host]
		if !ok {
			if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
				m.mu.Unlock()
				return
			}
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				m.mu.Unlock()
				return
			}
			dlf = &domainLogFile{f: f, path: logPath}
			m.files[host] = dlf
		}
		m.mu.Unlock()
	}

	// CLF-like format: remote - - [time] "method path" status bytes duration_ms "user_agent"
	line := fmt.Sprintf("%s - - [%s] \"%s %s\" %d %d %dms \"%s\"\n",
		remoteIP,
		time.Now().Format("02/Jan/2006:15:04:05 -0700"),
		method, path,
		status, bytes,
		duration.Milliseconds(),
		userAgent,
	)

	m.mu.RLock()
	_, _ = dlf.f.WriteString(line)
	dlf.written += int64(len(line))
	m.mu.RUnlock()
}

// Close closes all open log files.
func (m *domainLogManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dlf := range m.files {
		dlf.f.Close()
	}
	m.files = make(map[string]*domainLogFile)
}
