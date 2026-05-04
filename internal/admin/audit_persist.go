package admin

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	auditMaxFileSize  = 10 * 1024 * 1024 // rotate when log exceeds 10MB
	auditKeepRotated  = 3                // keep audit.log.1 .. audit.log.3 → ~40MB total history
)

func (s *Server) auditLogFile() string {
	if s.configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.configPath), "audit.log")
}

// loadAuditLog reads the persisted audit log (plus any rotated generations)
// and replays the last maxAuditEntries lines into the in-memory ring buffer.
// Reads oldest → newest so the tail kept in the buffer is the most recent
// 500 entries across all files. Caller must NOT hold auditMu — this method
// takes it.
func (s *Server) loadAuditLog() error {
	path := s.auditLogFile()
	if path == "" {
		return nil
	}

	// Order: audit.log.3, .2, .1, audit.log (oldest → newest).
	files := make([]string, 0, auditKeepRotated+1)
	for i := auditKeepRotated; i >= 1; i-- {
		files = append(files, fmt.Sprintf("%s.%d", path, i))
	}
	files = append(files, path)

	tail := make([]AuditEntry, 0, maxAuditEntries)
	for _, f := range files {
		if err := readAuditLines(f, &tail); err != nil {
			return err
		}
	}

	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	for i, e := range tail {
		s.auditEntries[i] = e
	}
	if len(tail) == maxAuditEntries {
		s.auditFull = true
		s.auditPos = 0
	} else {
		s.auditFull = false
		s.auditPos = len(tail)
	}
	return nil
}

// readAuditLines appends parsed entries from file at path to tail, dropping
// the oldest in-memory entry once tail reaches maxAuditEntries. Missing file
// is not an error.
func readAuditLines(path string, tail *[]AuditEntry) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip malformed lines
		}
		if len(*tail) == maxAuditEntries {
			*tail = (*tail)[1:]
		}
		*tail = append(*tail, e)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// appendAuditLine writes a single JSON line to the audit log file. Cheap
// best-effort: errors are logged but never propagate, so the API call that
// triggered the audit cannot fail because of disk problems.
func (s *Server) appendAuditLine(e AuditEntry) {
	path := s.auditLogFile()
	if path == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("audit log open failed", "err", err.Error())
		}
		return
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		if s.logger != nil {
			s.logger.Warn("audit log write failed", "err", err.Error())
		}
		return
	}
	info, statErr := f.Stat()
	_ = f.Close()
	// Windows cannot rename a file that still has an open handle, so rotation
	// must happen after Close.
	if statErr == nil && info.Size() > auditMaxFileSize {
		s.rotateAuditLog(path)
	}
}

// rotateAuditLog rolls audit.log → audit.log.1 → audit.log.2 → audit.log.3,
// dropping anything that would have become audit.log.4. Total on-disk
// history is therefore bounded at ~40MB (4 × auditMaxFileSize). Best-effort.
func (s *Server) rotateAuditLog(path string) {
	// Drop the oldest generation, then shift each .N → .(N+1).
	oldest := fmt.Sprintf("%s.%d", path, auditKeepRotated)
	_ = os.Remove(oldest)
	for i := auditKeepRotated - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		to := fmt.Sprintf("%s.%d", path, i+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) && s.logger != nil {
			s.logger.Warn("audit log rotate shift failed", "from", from, "err", err.Error())
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && s.logger != nil {
		s.logger.Warn("audit log rotate failed", "err", err.Error())
	}
}
