package admin

import (
	"net"
	"net/http"
	"time"
)

// AuditEntry represents a single admin API audit log entry.
type AuditEntry struct {
	Time    time.Time `json:"time"`
	Action  string    `json:"action"` // e.g., "config.reload", "domain.create"
	Detail  string    `json:"detail"` // e.g., "domain: example.com"
	IP      string    `json:"ip"`     // requester IP
	Success bool      `json:"success"`
}

const maxAuditEntries = 500

// rateLimitEntry tracks failed auth attempts for a single IP.
type rateLimitEntry struct {
	count     int
	firstFail time.Time
	blockedAt time.Time
	blocked   bool
}

const (
	rateLimitMaxFails  = 10
	rateLimitWindow    = 1 * time.Minute
	rateLimitBlockTime = 5 * time.Minute
	rateLimitCleanup   = 1 * time.Minute
)

// initAudit initialises the audit ring buffer and rate limiter fields.
// Called from New() after the Server is allocated.
func (s *Server) initAudit() {
	s.auditEntries = make([]AuditEntry, maxAuditEntries)
	s.rateLimit = make(map[string]*rateLimitEntry)

	// Start background cleanup goroutine for stale rate-limit entries.
	s.rlDone = make(chan struct{})
	go s.rateLimitCleaner()
}

// stopAudit stops the background rate-limit cleanup goroutine.
func (s *Server) stopAudit() {
	if s.rlDone != nil {
		close(s.rlDone)
	}
}

// RecordAudit appends an audit entry to the ring buffer. Safe for concurrent use.
func (s *Server) RecordAudit(action, detail, ip string, success bool) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	s.auditEntries[s.auditPos] = AuditEntry{
		Time:    time.Now(),
		Action:  action,
		Detail:  detail,
		IP:      ip,
		Success: success,
	}
	s.auditPos = (s.auditPos + 1) % maxAuditEntries
	if s.auditPos == 0 {
		s.auditFull = true
	}
}

// handleAudit returns the audit log entries in chronological order (oldest first).
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	var count int
	if s.auditFull {
		count = maxAuditEntries
	} else {
		count = s.auditPos
	}
	if count == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]\n"))
		return
	}

	var start int
	if s.auditFull {
		start = s.auditPos // oldest entry
	}

	result := make([]AuditEntry, 0, count)
	for i := 0; i < count; i++ {
		idx := (start + i) % maxAuditEntries
		result = append(result, s.auditEntries[idx])
	}
	jsonResponse(w, result)
}

// --- Rate limiting ---

// checkRateLimit returns true if the IP is currently blocked.
func (s *Server) checkRateLimit(ip string) bool {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	entry, ok := s.rateLimit[ip]
	if !ok {
		return false
	}

	if entry.blocked {
		if time.Since(entry.blockedAt) >= rateLimitBlockTime {
			// Block expired, reset.
			delete(s.rateLimit, ip)
			return false
		}
		return true
	}

	return false
}

// recordAuthFailure records a failed auth attempt for the given IP.
// Returns true if the IP is now blocked.
func (s *Server) recordAuthFailure(ip string) bool {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	now := time.Now()
	entry, ok := s.rateLimit[ip]
	if !ok {
		s.rateLimit[ip] = &rateLimitEntry{
			count:     1,
			firstFail: now,
		}
		return false
	}

	// If the window has expired, reset the counter.
	if now.Sub(entry.firstFail) > rateLimitWindow {
		entry.count = 1
		entry.firstFail = now
		entry.blocked = false
		return false
	}

	entry.count++
	if entry.count >= rateLimitMaxFails {
		entry.blocked = true
		entry.blockedAt = now
		return true
	}

	return false
}

// rateLimitCleaner runs in the background and removes stale entries every minute.
func (s *Server) rateLimitCleaner() {
	ticker := time.NewTicker(rateLimitCleanup)
	defer ticker.Stop()

	for {
		select {
		case <-s.rlDone:
			return
		case <-ticker.C:
			s.cleanupRateLimits()
		}
	}
}

// cleanupRateLimits removes expired rate-limit entries.
func (s *Server) cleanupRateLimits() {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	now := time.Now()
	for ip, entry := range s.rateLimit {
		if entry.blocked {
			if now.Sub(entry.blockedAt) >= rateLimitBlockTime {
				delete(s.rateLimit, ip)
			}
		} else {
			if now.Sub(entry.firstFail) > rateLimitWindow {
				delete(s.rateLimit, ip)
			}
		}
	}
}

// requestIP extracts the client IP from a request, stripping the port.
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimitMap returns the rate-limit map for testing purposes.
func (s *Server) RateLimitMap() map[string]*rateLimitEntry {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	return s.rateLimit
}

// AuditMu is exported for testing to allow direct field access is not needed;
// we use exported accessors instead.
