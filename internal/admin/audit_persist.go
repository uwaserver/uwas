package admin

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const auditMaxFileSize = 10 * 1024 * 1024 // rotate when log exceeds 10MB

func (s *Server) auditLogFile() string {
	if s.configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.configPath), "audit.log")
}

// loadAuditLog reads the persisted audit log and replays the last
// maxAuditEntries lines into the in-memory ring buffer. Caller must NOT hold
// auditMu — this method takes it.
func (s *Server) loadAuditLog() error {
	path := s.auditLogFile()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	tail := make([]AuditEntry, 0, maxAuditEntries)
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
		if len(tail) == maxAuditEntries {
			tail = tail[1:]
		}
		tail = append(tail, e)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
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

// rotateAuditLog renames audit.log → audit.log.1 (overwriting any previous
// rotation) and starts a fresh log file. Best-effort.
func (s *Server) rotateAuditLog(path string) {
	old := path + ".1"
	_ = os.Remove(old)
	if err := os.Rename(path, old); err != nil && s.logger != nil {
		s.logger.Warn("audit log rotate failed", "err", err.Error())
	}
}
