package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultMaxLogSize = 50 * 1024 * 1024 // 50MB per log file
	maxLogBackups     = 5                 // keep 5 rotated files
)

// domainLogManager manages per-domain access log files with rotation.
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
func (m *domainLogManager) Write(host, logPath string, method, path, remoteIP, userAgent string, status, bytes int, duration time.Duration) {
	if logPath == "" {
		return
	}

	m.mu.RLock()
	dlf, ok := m.files[host]
	m.mu.RUnlock()

	if !ok {
		m.mu.Lock()
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
			// Get current file size
			info, _ := f.Stat()
			written := int64(0)
			if info != nil {
				written = info.Size()
			}
			dlf = &domainLogFile{f: f, path: logPath, written: written}
			m.files[host] = dlf
		}
		m.mu.Unlock()
	}

	// CLF-like format
	line := fmt.Sprintf("%s - - [%s] \"%s %s\" %d %d %dms \"%s\"\n",
		remoteIP,
		time.Now().Format("02/Jan/2006:15:04:05 -0700"),
		method, path,
		status, bytes,
		duration.Milliseconds(),
		userAgent,
	)

	m.mu.Lock()
	_, _ = dlf.f.WriteString(line)
	dlf.written += int64(len(line))

	// Rotate if file exceeds max size
	if dlf.written >= defaultMaxLogSize {
		m.rotate(host, dlf)
	}
	m.mu.Unlock()
}

// rotate renames the current log file and opens a new one.
func (m *domainLogManager) rotate(host string, dlf *domainLogFile) {
	dlf.f.Close()

	// Shift existing backups: .4→.5(delete), .3→.4, .2→.3, .1→.2, current→.1
	for i := maxLogBackups; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", dlf.path, i)
		if i == maxLogBackups {
			os.Remove(src) // delete oldest
		} else {
			dst := fmt.Sprintf("%s.%d", dlf.path, i+1)
			os.Rename(src, dst)
		}
	}
	os.Rename(dlf.path, dlf.path+".1")

	// Open fresh log file
	f, err := os.OpenFile(dlf.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		delete(m.files, host)
		return
	}
	dlf.f = f
	dlf.written = 0
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
