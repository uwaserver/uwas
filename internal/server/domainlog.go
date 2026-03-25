package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

const (
	defaultMaxLogSize    = 50 * 1024 * 1024 // 50MB per log file
	defaultMaxBackups    = 5
	defaultMaxAge        = 30 * 24 * time.Hour // 30 days
	cleanupInterval      = 1 * time.Hour
	rotatedTimeFormat    = "20060102-150405"
)

// domainLogManager manages per-domain access log files with rotation.
type domainLogManager struct {
	mu    sync.RWMutex
	files map[string]*domainLogFile
	stop  chan struct{}
}

type domainLogFile struct {
	f       *os.File
	path    string
	written int64
	rotate  config.RotateConfig
}

func newDomainLogManager() *domainLogManager {
	return &domainLogManager{
		files: make(map[string]*domainLogFile),
		stop:  make(chan struct{}),
	}
}

// StartCleanup starts the background goroutine that removes old rotated logs.
// Should be called once after server initialization.
func (m *domainLogManager) StartCleanup() {
	go m.cleanupLoop()
}

// Write writes an access log entry for the given domain.
func (m *domainLogManager) Write(host, logPath string, rotate config.RotateConfig, method, path, remoteIP, userAgent string, status, bytes int, duration time.Duration) {
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
			info, _ := f.Stat()
			written := int64(0)
			if info != nil {
				written = info.Size()
			}
			dlf = &domainLogFile{f: f, path: logPath, written: written, rotate: rotate}
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

	maxSize := int64(dlf.rotate.MaxSize)
	if maxSize <= 0 {
		maxSize = defaultMaxLogSize
	}
	if dlf.written >= maxSize {
		m.rotate(host, dlf)
	}
	m.mu.Unlock()
}

// rotate closes the current log, renames it with a timestamp, compresses it,
// and opens a fresh log file.
func (m *domainLogManager) rotate(host string, dlf *domainLogFile) {
	dlf.f.Close()

	// Rename current → timestamped .gz
	ts := time.Now().Format(rotatedTimeFormat)
	rotatedName := fmt.Sprintf("%s.%s", dlf.path, ts)
	if err := os.Rename(dlf.path, rotatedName); err == nil {
		go compressFile(rotatedName) // compress in background
	}

	// Enforce max backups
	maxBackups := dlf.rotate.MaxBackups
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}
	go pruneBackups(dlf.path, maxBackups)

	// Open fresh log file
	f, err := os.OpenFile(dlf.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		delete(m.files, host)
		return
	}
	dlf.f = f
	dlf.written = 0
}

// cleanupLoop periodically removes rotated logs older than MaxAge.
func (m *domainLogManager) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.cleanupOld()
		case <-m.stop:
			return
		}
	}
}

// cleanupOld removes rotated log files older than their configured MaxAge.
func (m *domainLogManager) cleanupOld() {
	m.mu.RLock()
	// Snapshot paths and configs
	type entry struct {
		path   string
		maxAge time.Duration
	}
	var entries []entry
	for _, dlf := range m.files {
		maxAge := dlf.rotate.MaxAge.Duration
		if maxAge <= 0 {
			maxAge = defaultMaxAge
		}
		entries = append(entries, entry{path: dlf.path, maxAge: maxAge})
	}
	m.mu.RUnlock()

	cutoff := time.Now()
	for _, e := range entries {
		rotated := findRotatedFiles(e.path)
		for _, rf := range rotated {
			info, err := os.Stat(rf)
			if err != nil {
				continue
			}
			if cutoff.Sub(info.ModTime()) > e.maxAge {
				os.Remove(rf)
			}
		}
	}
}

// Close closes all open log files and stops the cleanup goroutine.
func (m *domainLogManager) Close() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dlf := range m.files {
		dlf.f.Close()
	}
	m.files = make(map[string]*domainLogFile)
}

// compressFile gzips a file in-place (src → src.gz, then removes src).
func compressFile(path string) {
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()

	dst, err := os.Create(path + ".gz")
	if err != nil {
		return
	}

	gz := gzip.NewWriter(dst)
	if _, err := io.Copy(gz, src); err != nil {
		gz.Close()
		dst.Close()
		os.Remove(path + ".gz")
		return
	}
	gz.Close()
	dst.Close()
	src.Close()
	os.Remove(path) // remove uncompressed
}

// pruneBackups keeps only the newest maxBackups rotated files.
func pruneBackups(basePath string, maxBackups int) {
	rotated := findRotatedFiles(basePath)
	if len(rotated) <= maxBackups {
		return
	}
	// Sort newest first by name (timestamp in name ensures correct order)
	sort.Sort(sort.Reverse(sort.StringSlice(rotated)))
	for _, old := range rotated[maxBackups:] {
		os.Remove(old)
	}
}

// findRotatedFiles returns all rotated log files for the given base path.
// Matches patterns: base.YYYYMMDD-HHMMSS, base.YYYYMMDD-HHMMSS.gz, base.N (legacy)
func findRotatedFiles(basePath string) []string {
	dir := filepath.Dir(basePath)
	base := filepath.Base(basePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var rotated []string
	for _, e := range entries {
		name := e.Name()
		if name == base {
			continue // skip the active log
		}
		if strings.HasPrefix(name, base+".") {
			rotated = append(rotated, filepath.Join(dir, name))
		}
	}
	return rotated
}
