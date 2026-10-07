package server

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
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
	defaultMaxLogSize = 50 * 1024 * 1024 // 50MB per log file
	defaultMaxBackups = 5
	defaultMaxAge     = 30 * 24 * time.Hour // 30 days
	cleanupInterval   = 1 * time.Hour
	// A buffered log must not sit unwritten while traffic is light: a domain
	// with a request a minute and a 64KB buffer would show nothing to
	// `tail -f` until shutdown.
	logFlushInterval  = 1 * time.Second
	rotatedTimeFormat = "20060102-150405.000000000"
)

// domainLogManager manages per-domain access log files with rotation.
//
// Locking: the manager's mu protects the files map only. Each
// domainLogFile carries its own mutex (dlf.mu), so write/rotate work for
// host A no longer blocks write work for host B. Within a single host,
// writes remain serialized — file-line ordering depends on it.
// Refs: refactor.md P15.
type domainLogManager struct {
	mu    sync.RWMutex
	files map[string]*domainLogFile
	stop  chan struct{}
	bg    sync.WaitGroup
}

type domainLogFile struct {
	mu      sync.Mutex
	f       *os.File
	path    string
	written int64
	rotate  config.RotateConfig
	// buf wraps f when access_log.buffer_size is set. nil means unbuffered,
	// which stays the default: a buffered log loses its tail if the process
	// dies, so the operator has to ask for it.
	buf *bufio.Writer
}

// writer returns the sink for a line: the buffer when one is configured.
func (d *domainLogFile) writer() io.Writer {
	if d.buf != nil {
		return d.buf
	}
	return d.f
}

// flushLocked empties the buffer. Caller must hold d.mu.
func (d *domainLogFile) flushLocked() {
	if d.buf != nil {
		_ = d.buf.Flush()
	}
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
	go m.flushLoop()
}

// flushLoop empties buffered logs on a timer. Files with no buffer are
// untouched, so this costs nothing for the default configuration.
func (m *domainLogManager) flushLoop() {
	t := time.NewTicker(logFlushInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.flushAll()
		}
	}
}

// FlushAll empties every buffered log immediately.
func (m *domainLogManager) flushAll() {
	m.mu.RLock()
	files := make([]*domainLogFile, 0, len(m.files))
	for _, dlf := range m.files {
		files = append(files, dlf)
	}
	m.mu.RUnlock()

	for _, dlf := range files {
		dlf.mu.Lock()
		dlf.flushLocked()
		dlf.mu.Unlock()
	}
}

// Write writes an access log entry for the given domain.
func (m *domainLogManager) Write(host string, cfg config.AccessLogConfig, method, path, remoteIP, userAgent string, status, bytes int, duration time.Duration) {
	logPath := cfg.Path
	if logPath == "" {
		return
	}
	rotate := cfg.Rotate

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
			if cfg.BufferSize > 0 {
				dlf.buf = bufio.NewWriterSize(f, cfg.BufferSize)
			}
			m.files[host] = dlf
		}
		m.mu.Unlock()
	}

	line := accessLogLine(cfg.Format, time.Now(), method, path, remoteIP, userAgent, status, bytes, duration)

	// Per-host lock: writes for different domains run in parallel; only
	// the same domain's writes serialize (needed for correct line order).
	dlf.mu.Lock()
	_, _ = io.WriteString(dlf.writer(), line)
	dlf.written += int64(len(line))

	maxSize := int64(dlf.rotate.MaxSize)
	if maxSize <= 0 {
		maxSize = defaultMaxLogSize
	}
	needsRotate := dlf.written >= maxSize
	reopenFailed := false
	if needsRotate {
		// Flush first: rotation renames the file out from under the buffer,
		// and anything still pending would be written into the new one.
		dlf.flushLocked()
		reopenFailed = m.rotateLocked(dlf)
	}
	dlf.mu.Unlock()

	// Drop the entry only once dlf.mu is released. Taking m.mu while still
	// holding dlf.mu would invert the order Close uses (m.mu, then dlf.mu)
	// and deadlock the two against each other.
	if reopenFailed {
		m.mu.Lock()
		delete(m.files, host)
		m.mu.Unlock()
	}
}

// rotateLocked closes the current log, renames it with a timestamp,
// compresses it, and opens a fresh log file. Caller must hold dlf.mu.
//
// It returns true when the fresh log file could not be reopened. The caller
// is responsible for dropping the manager's map entry at that point -- and
// must do so only after releasing dlf.mu.
//
// rotateLocked deliberately never takes m.mu. Close acquires them in the
// opposite order (m.mu, then dlf.mu), so a m.mu acquisition here while the
// caller holds dlf.mu is an ABBA inversion that deadlocks the pair: Close
// waits for dlf.mu while rotateLocked waits for m.mu. Keeping the map
// mutation in the caller removes the inversion entirely.
func (m *domainLogManager) rotateLocked(dlf *domainLogFile) (reopenFailed bool) {
	dlf.flushLocked()
	dlf.f.Close()

	// Rename current → timestamped .gz
	ts := time.Now().Format(rotatedTimeFormat)
	rotatedName := fmt.Sprintf("%s.%s", dlf.path, ts)
	if err := os.Rename(dlf.path, rotatedName); err == nil {
		m.bg.Add(1)
		go func() {
			defer m.bg.Done()
			compressFile(rotatedName)
		}()
	}

	// Enforce max backups
	maxBackups := dlf.rotate.MaxBackups
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}
	m.bg.Add(1)
	go func() {
		defer m.bg.Done()
		pruneBackups(dlf.path, maxBackups)
	}()

	// Open fresh log file
	f, err := domainLogOpenFile(dlf.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return true
	}
	dlf.f = f
	if dlf.buf != nil {
		dlf.buf.Reset(f)
	}
	dlf.written = 0
	return false
}

// domainLogNow is the clock used to age rotated logs. Indirected so tests
// can pin the cleanupOld MaxAge boundary exactly instead of racing the real
// clock, which cannot express "expired at precisely maxAge".
var domainLogNow = time.Now

// domainLogCloseFile closes a compression destination. Indirected because
// close() on a regular file does not report the write errors that /dev/full
// and a full filesystem produce -- those surface at write time, hitting the
// earlier branches -- so the dst.Close() failure branch is otherwise
// unreachable from any local filesystem.
var domainLogCloseFile = func(f *os.File) error { return f.Close() }

// domainLogOpenFile opens the fresh active log after a rotation. Indirected so
// tests can drive rotateLocked's reopen-failure branch, whose realistic
// trigger is ENOSPC on a full disk -- a condition needing a size-limited mount
// to reproduce, which the test environment cannot create. Every attempt to
// force it through the filesystem instead was defeated: rotateLocked renames
// the active log aside first (so a directory or dangling symlink on the path
// is simply moved away and recreated), and dlf.f.Close() frees a descriptor
// before the reopen, which defeats FD exhaustion.
var domainLogOpenFile = os.OpenFile

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

	cutoff := domainLogNow()
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
// Each domainLogFile's own mutex is acquired so an in-flight Write
// finishes before its file handle is closed.
func (m *domainLogManager) Close() {
	close(m.stop)
	m.mu.Lock()
	for _, dlf := range m.files {
		dlf.mu.Lock()
		dlf.flushLocked()
		dlf.f.Close()
		dlf.mu.Unlock()
	}
	m.files = make(map[string]*domainLogFile)
	m.mu.Unlock()
	m.bg.Wait()
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
	// gz.Close() flushes the gzip trailer (CRC32 + size). If it fails the
	// .gz is corrupt — keep the original log and remove the bad archive.
	if err := gz.Close(); err != nil {
		dst.Close()
		os.Remove(path + ".gz")
		return
	}
	if err := domainLogCloseFile(dst); err != nil {
		os.Remove(path + ".gz")
		return
	}
	src.Close()
	os.Remove(path) // remove uncompressed
}

// pruneBackups keeps only the newest maxBackups rotated logs.
func pruneBackups(basePath string, maxBackups int) {
	rotated := findRotatedFiles(basePath)
	if len(rotated) == 0 {
		return
	}

	// Group by rotation identity. compressFile holds "base.<ts>" and its
	// "base.<ts>.gz" on disk simultaneously for the whole copy window (it only
	// unlinks the source after the archive is closed), and rotateLocked starts
	// it and this prune concurrently. Counting the pair as two backups let the
	// keep-slice absorb the in-flight source and evict an archive that was
	// still inside the retention window, so a configured max_backups silently
	// lost one of the logs it was supposed to keep. Stripping the .gz suffix
	// collapses each pair back to the single rotated log it represents; a
	// backup whose compression failed is still its own group and is retained.
	groups := make(map[string][]string, len(rotated))
	var order []string
	for _, path := range rotated {
		id := strings.TrimSuffix(path, ".gz")
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], path)
	}
	if len(order) <= maxBackups {
		return
	}
	// Sort newest first by name (timestamp in name ensures correct order)
	sort.Sort(sort.Reverse(sort.StringSlice(order)))
	for _, id := range order[maxBackups:] {
		for _, path := range groups[id] {
			os.Remove(path)
		}
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

// accessLogLine renders one entry in the configured format.
//
// access_log.format and access_log.buffer_size were dead configuration: the
// writer always emitted an unbuffered CLF-like line and read neither field,
// while SPECIFICATION.md documents `format: json | clf | custom` and a
// buffer size. Only path and rotate ever reached this code.
//
// "custom" is documented but there is no format-string field to carry a
// template, so it falls back to clf rather than pretending. An unrecognised
// value does the same: a log is not the place to fail a request over a typo.
func accessLogLine(format string, now time.Time, method, path, remoteIP, userAgent string, status, bytes int, duration time.Duration) string {
	if strings.EqualFold(strings.TrimSpace(format), "json") {
		entry := struct {
			Time       string `json:"time"`
			RemoteIP   string `json:"remote_ip"`
			Method     string `json:"method"`
			Path       string `json:"path"`
			Status     int    `json:"status"`
			Bytes      int    `json:"bytes"`
			DurationMS int64  `json:"duration_ms"`
			UserAgent  string `json:"user_agent"`
		}{
			Time:       now.Format(time.RFC3339Nano),
			RemoteIP:   remoteIP,
			Method:     method,
			Path:       path,
			Status:     status,
			Bytes:      bytes,
			DurationMS: duration.Milliseconds(),
			UserAgent:  userAgent,
		}
		// Marshal cannot fail for this struct; fall through to clf if it ever
		// does rather than dropping the line.
		if data, err := json.Marshal(entry); err == nil {
			return string(data) + "\n"
		}
	}

	// CLF-like format: the default, and the fallback for clf/custom/unknown.
	return fmt.Sprintf("%s - - [%s] \"%s %s\" %d %d %dms \"%s\"\n",
		remoteIP,
		now.Format("02/Jan/2006:15:04:05 -0700"),
		method, path,
		status, bytes,
		duration.Milliseconds(),
		userAgent,
	)
}

// KnownAccessLogFormat reports whether a configured format is one this writer
// renders. An unknown value falls back to clf; the caller warns once at
// startup rather than per request, and never blocks the server from booting
// over a field that did nothing until now.
func KnownAccessLogFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "clf", "json", "custom":
		return true
	default:
		return false
	}
}
