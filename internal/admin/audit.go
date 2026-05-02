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

// rateLimitEntry tracks failed auth attempts for a single IP or username.
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
	s.userRateLimits = make(map[string]*rateLimitEntry)

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
// IP address is only recorded if audit.RecordIP is enabled in config (GDPR compliance).
func (s *Server) RecordAudit(action, detail, ip string, success bool) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	entryIP := ip
	if !s.config.Global.Audit.RecordIP {
		entryIP = "" // redact IP when consent is disabled
	}

	s.auditEntries[s.auditPos] = AuditEntry{
		Time:    time.Now(),
		Action:  action,
		Detail:  detail,
		IP:      entryIP,
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
	var start int
	if s.auditFull {
		start = s.auditPos // oldest entry
	}

	result := make([]AuditEntry, 0, count)
	for i := 0; i < count; i++ {
		idx := (start + i) % maxAuditEntries
		result = append(result, s.auditEntries[idx])
	}

	limit, offset := parsePagination(r)
	result, total := paginateSlice(result, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  result,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// --- Rate limiting ---

// checkRateLimit returns true if the IP or username is currently blocked.
// Pass an empty username to check only the IP.
func (s *Server) checkRateLimit(ip, username string) bool {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	// Check IP-based rate limit.
	if blocked := s.isBlocked(s.rateLimit, ip); blocked {
		return true
	}

	// Check username-based rate limit.
	if username != "" {
		if blocked := s.isBlocked(s.userRateLimits, username); blocked {
			return true
		}
	}

	return false
}

// isBlocked checks if a key in the given map is currently blocked.
// Caller must hold s.rlMu.
func (s *Server) isBlocked(m map[string]*rateLimitEntry, key string) bool {
	entry, ok := m[key]
	if !ok {
		return false
	}

	if entry.blocked {
		if time.Since(entry.blockedAt) >= rateLimitBlockTime {
			// Block expired, reset.
			delete(m, key)
			return false
		}
		return true
	}

	return false
}

// recordAuthFailure records a failed auth attempt for the given IP and
// optionally for a username. Returns true if either the IP or username is
// now blocked.
// Pass an empty username to record only the IP failure.
func (s *Server) recordAuthFailure(ip, username string) bool {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	now := time.Now()
	ipBlocked := s.recordFailure(s.rateLimit, ip, now)

	userBlocked := false
	if username != "" {
		userBlocked = s.recordFailure(s.userRateLimits, username, now)
	}

	return ipBlocked || userBlocked
}

// recordFailure increments the failure count for a key in the given map.
// Returns true if the key is now blocked.
// Caller must hold s.rlMu.
func (s *Server) recordFailure(m map[string]*rateLimitEntry, key string, now time.Time) bool {
	entry, ok := m[key]
	if !ok {
		m[key] = &rateLimitEntry{
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

// cleanupRateLimits removes expired rate-limit entries for both IP and username maps.
func (s *Server) cleanupRateLimits() {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	now := time.Now()
	s.cleanupMap(s.rateLimit, now)
	s.cleanupMap(s.userRateLimits, now)
}

// cleanupMap removes expired entries from a rate-limit map.
// Caller must hold s.rlMu.
func (s *Server) cleanupMap(m map[string]*rateLimitEntry, now time.Time) {
	for key, entry := range m {
		if entry.blocked {
			if now.Sub(entry.blockedAt) >= rateLimitBlockTime {
				delete(m, key)
			}
		} else {
			if now.Sub(entry.firstFail) > rateLimitWindow {
				delete(m, key)
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

// RateLimitMap returns the IP rate-limit map for testing purposes.
func (s *Server) RateLimitMap() map[string]*rateLimitEntry {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	return s.rateLimit
}

// UserRateLimitMap returns the username rate-limit map for testing purposes.
func (s *Server) UserRateLimitMap() map[string]*rateLimitEntry {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	return s.userRateLimits
}

// AuditMu is exported for testing to allow direct field access is not needed;
// we use exported accessors instead.
