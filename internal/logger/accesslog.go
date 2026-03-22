package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AccessLogger writes access logs to a file with rotation support.
type AccessLogger struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	format      string
	maxSize     int64 // max file size in bytes before rotation (0 = no rotation)
	written     int64
	maxBackups  int
}

// AccessLogConfig configures the access logger.
type AccessLogConfig struct {
	Path       string
	Format     string // "json" or "clf"
	MaxSize    int64  // bytes, 0 = no rotation
	MaxBackups int    // number of rotated files to keep
}

// NewAccessLogger opens or creates an access log file.
func NewAccessLogger(cfg AccessLogConfig) (*AccessLogger, error) {
	if cfg.Path == "" {
		return nil, nil
	}

	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}

	f, err := os.OpenFile(cfg.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open access log %s: %w", cfg.Path, err)
	}

	format := cfg.Format
	if format == "" {
		format = "json"
	}

	maxBackups := cfg.MaxBackups
	if maxBackups <= 0 {
		maxBackups = 5
	}

	// Get current file size
	info, _ := f.Stat()
	var written int64
	if info != nil {
		written = info.Size()
	}

	return &AccessLogger{
		file:       f,
		path:       cfg.Path,
		format:     format,
		maxSize:    cfg.MaxSize,
		written:    written,
		maxBackups: maxBackups,
	}, nil
}

// Log writes an access log entry. Rotates the file if maxSize is exceeded.
func (a *AccessLogger) Log(method, host, path, remoteIP, userAgent, requestID string,
	status int, bytes int64, durationMs, ttfbMs int64) {

	var line string
	now := time.Now()

	switch a.format {
	case "clf":
		line = fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" %d %d \"-\" \"%s\"\n",
			remoteIP,
			now.Format("02/Jan/2006:15:04:05 -0700"),
			method, path, status, bytes, userAgent)
	default:
		line = fmt.Sprintf(`{"time":"%s","method":"%s","host":"%s","path":"%s","status":%d,"bytes":%d,"duration_ms":%d,"ttfb_ms":%d,"remote":"%s","user_agent":"%s","request_id":"%s"}`+"\n",
			now.Format(time.RFC3339), method, host, path, status, bytes,
			durationMs, ttfbMs, remoteIP, userAgent, requestID)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.file.WriteString(line)
	a.written += int64(len(line))

	// Rotate if needed
	if a.maxSize > 0 && a.written >= a.maxSize {
		a.rotate()
	}
}

// rotate closes the current file, renames it with a timestamp, and opens a new one.
func (a *AccessLogger) rotate() {
	a.file.Close()

	// Rename current → timestamped backup
	ts := time.Now().Format("20060102-150405")
	backupPath := a.path + "." + ts
	os.Rename(a.path, backupPath)

	// Remove old backups beyond maxBackups
	a.cleanOldBackups()

	// Open new file
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	a.file = f
	a.written = 0
}

// cleanOldBackups keeps only the newest maxBackups rotated files.
func (a *AccessLogger) cleanOldBackups() {
	dir := filepath.Dir(a.path)
	base := filepath.Base(a.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var backups []string
	for _, e := range entries {
		name := e.Name()
		if name != base && len(name) > len(base) && name[:len(base)] == base {
			backups = append(backups, filepath.Join(dir, name))
		}
	}

	// Remove oldest backups if over limit
	for len(backups) > a.maxBackups {
		os.Remove(backups[0])
		backups = backups[1:]
	}
}

// Reopen closes and reopens the log file (for SIGHUP-based rotation by external tools).
func (a *AccessLogger) Reopen() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.file != nil {
		a.file.Close()
	}

	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	a.file = f
	a.written = 0
	return nil
}

// Close flushes and closes the log file.
func (a *AccessLogger) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Close()
}
