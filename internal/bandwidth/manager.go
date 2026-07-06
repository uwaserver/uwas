package bandwidth

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// Manager tracks bandwidth usage per domain and enforces limits.
type Manager struct {
	mu      sync.RWMutex
	limits  map[string]config.BandwidthConfig // host -> config
	usage   map[string]*DomainUsage           // host -> usage
	alertFn func(host string, limitType string, current, limit int64)
}

// DomainUsage tracks bandwidth usage for a single domain.
// Counters use atomic.Int64 to prevent race conditions on concurrent updates.
type DomainUsage struct {
	mu           sync.Mutex
	MonthlyBytes atomic.Int64 `json:"-"`
	DailyBytes   atomic.Int64 `json:"-"`
	LastReset    time.Time    `json:"last_reset"`
	DailyReset   time.Time    `json:"daily_reset"`
	LastUpdated  time.Time    `json:"last_updated"`
	Blocked      bool         `json:"blocked"`
	Throttled    bool         `json:"throttled"`
}

// Status represents the current bandwidth status for a domain.
type Status struct {
	Host         string    `json:"host"`
	MonthlyBytes int64     `json:"monthly_bytes"`
	DailyBytes   int64     `json:"daily_bytes"`
	MonthlyLimit int64     `json:"monthly_limit"`
	DailyLimit   int64     `json:"daily_limit"`
	MonthlyPct   float64   `json:"monthly_pct"`
	DailyPct     float64   `json:"daily_pct"`
	Blocked      bool      `json:"blocked"`
	Throttled    bool      `json:"throttled"`
	LastReset    time.Time `json:"last_reset"`
	DailyReset   time.Time `json:"daily_reset"`
}

// NewManager creates a new bandwidth manager.
func NewManager(domains []config.Domain) *Manager {
	m := &Manager{
		limits: make(map[string]config.BandwidthConfig),
		usage:  make(map[string]*DomainUsage),
	}
	m.UpdateDomains(domains)
	return m
}

// UpdateDomains updates the bandwidth configurations from domain list.
func (m *Manager) UpdateDomains(domains []config.Domain) {
	m.mu.Lock()
	defer m.mu.Unlock()

	newLimits := make(map[string]config.BandwidthConfig)
	for _, d := range domains {
		if d.Bandwidth.Enabled {
			newLimits[d.Host] = d.Bandwidth
			// Initialize usage tracking if not exists
			if _, ok := m.usage[d.Host]; !ok {
				m.usage[d.Host] = &DomainUsage{
					LastReset:  time.Now(),
					DailyReset: time.Now(),
				}
			}
		}
	}
	m.limits = newLimits
}

// Record records bandwidth usage for a domain.
// Returns true if the request should be blocked.
// normalizeHost strips any port and lowercases the host so lookups match the
// map keys, which are the configured domain hosts (normalized). The live
// dispatch path records usage with the raw request Host — which may carry a
// port (example.com:8443) or mixed case — so callers must not be relied on to
// pre-normalize.
func normalizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

func (m *Manager) Record(host string, bytes int64) (blocked bool, throttled bool) {
	host = normalizeHost(host)
	m.mu.RLock()
	limit, hasLimit := m.limits[host]
	usage, hasUsage := m.usage[host]
	alertFn := m.alertFn
	m.mu.RUnlock()

	if !hasLimit || !limit.Enabled {
		return false, false
	}

	if !hasUsage {
		return false, false
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()

	now := time.Now()

	// Check if we need to reset counters using atomic CAS for window swap
	if now.Sub(usage.LastReset) > 30*24*time.Hour {
		usage.MonthlyBytes.Store(0)
		usage.LastReset = now
		usage.Blocked = false
		usage.Throttled = false
	}
	if now.Sub(usage.DailyReset) > 24*time.Hour {
		usage.DailyBytes.Store(0)
		usage.DailyReset = now
		usage.Blocked = false
		usage.Throttled = false
	}

	// Add bytes atomically
	usage.MonthlyBytes.Add(bytes)
	usage.DailyBytes.Add(bytes)
	usage.LastUpdated = now

	// Load current values for limit checks
	monthlyBytes := usage.MonthlyBytes.Load()
	dailyBytes := usage.DailyBytes.Load()

	// Check limits
	monthlyLimit := int64(limit.MonthlyLimit)
	dailyLimit := int64(limit.DailyLimit)

	// Send alerts when usage CROSSES a threshold this call. Comparing the
	// pre-add value to the post-add value fires exactly once per crossing
	// regardless of jump size — the old razor-thin band (pct >= 0.9 && < 0.91)
	// silently missed the alert whenever a single record jumped the ratio past
	// the band (e.g. a 100MB response against a 1GB limit).
	if alertFn != nil {
		crossed := func(prev, cur, lim int64, frac float64) bool {
			threshold := frac * float64(lim)
			return float64(prev) < threshold && float64(cur) >= threshold
		}
		if monthlyLimit > 0 {
			prev := monthlyBytes - bytes
			if crossed(prev, monthlyBytes, monthlyLimit, 1.0) {
				alertFn(host, "monthly_exceeded", monthlyBytes, monthlyLimit)
			} else if crossed(prev, monthlyBytes, monthlyLimit, 0.9) {
				alertFn(host, "monthly_90", monthlyBytes, monthlyLimit)
			}
		}
		if dailyLimit > 0 {
			prev := dailyBytes - bytes
			if crossed(prev, dailyBytes, dailyLimit, 1.0) {
				alertFn(host, "daily_exceeded", dailyBytes, dailyLimit)
			} else if crossed(prev, dailyBytes, dailyLimit, 0.9) {
				alertFn(host, "daily_90", dailyBytes, dailyLimit)
			}
		}
	}

	// Block if exceeded hard limit
	if limit.Action == "block" {
		if (monthlyLimit > 0 && monthlyBytes >= monthlyLimit) ||
			(dailyLimit > 0 && dailyBytes >= dailyLimit) {
			usage.Blocked = true
			return true, false
		}
	}

	// Throttle if exceeded threshold (80% for throttle, 100% for block)
	if limit.Action == "throttle" || limit.Action == "" {
		throttleThreshold := int64(float64(monthlyLimit) * 0.8)
		if monthlyLimit > 0 && monthlyBytes >= throttleThreshold {
			usage.Throttled = true
			return false, true
		}
		throttleThreshold = int64(float64(dailyLimit) * 0.8)
		if dailyLimit > 0 && dailyBytes >= throttleThreshold {
			usage.Throttled = true
			return false, true
		}
	}

	return false, false
}

// SetAlertFunc sets the alert callback function.
func (m *Manager) SetAlertFunc(fn func(host string, limitType string, current, limit int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alertFn = fn
}

// IsBlocked returns true if the domain has exceeded its bandwidth limit.
func (m *Manager) IsBlocked(host string) bool {
	m.mu.RLock()
	usage, ok := m.usage[host]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	usage.mu.Lock()
	defer usage.mu.Unlock()
	return usage.Blocked
}

// GetStatus returns the bandwidth status for a domain.
func (m *Manager) GetStatus(host string) *Status {
	m.mu.RLock()
	limit, hasLimit := m.limits[host]
	usage, hasUsage := m.usage[host]
	m.mu.RUnlock()

	if !hasLimit || !hasUsage {
		return nil
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()

	monthlyBytes := usage.MonthlyBytes.Load()
	dailyBytes := usage.DailyBytes.Load()
	monthlyLimit := int64(limit.MonthlyLimit)
	dailyLimit := int64(limit.DailyLimit)

	status := &Status{
		Host:         host,
		MonthlyBytes: monthlyBytes,
		DailyBytes:   dailyBytes,
		MonthlyLimit: monthlyLimit,
		DailyLimit:   dailyLimit,
		Blocked:      usage.Blocked,
		Throttled:    usage.Throttled,
		LastReset:    usage.LastReset,
		DailyReset:   usage.DailyReset,
	}

	if monthlyLimit > 0 {
		status.MonthlyPct = float64(monthlyBytes) / float64(monthlyLimit) * 100
	}
	if dailyLimit > 0 {
		status.DailyPct = float64(dailyBytes) / float64(dailyLimit) * 100
	}

	return status
}

// GetAllStatus returns bandwidth status for all domains.
func (m *Manager) GetAllStatus() []Status {
	m.mu.RLock()
	hosts := make([]string, 0, len(m.limits))
	for host := range m.limits {
		hosts = append(hosts, host)
	}
	m.mu.RUnlock()

	var statuses []Status
	for _, host := range hosts {
		if status := m.GetStatus(host); status != nil {
			statuses = append(statuses, *status)
		}
	}
	return statuses
}

// Reset resets the usage counters for a domain.
func (m *Manager) Reset(host string) {
	m.mu.RLock()
	usage, ok := m.usage[host]
	m.mu.RUnlock()

	if !ok {
		return
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()
	usage.MonthlyBytes.Store(0)
	usage.DailyBytes.Store(0)
	usage.LastReset = time.Now()
	usage.DailyReset = time.Now()
	usage.Blocked = false
	usage.Throttled = false
}

// Middleware returns an HTTP middleware that checks bandwidth limits.
func (m *Manager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Strip port and lowercase so lookups match the configured hosts.
			host := normalizeHost(r.Host)

			m.mu.RLock()
			_, hasLimit := m.limits[host]
			m.mu.RUnlock()

			if hasLimit {
				// Check if blocked before processing
				m.mu.RLock()
				usage := m.usage[host]
				m.mu.RUnlock()

				if usage != nil {
					usage.mu.Lock()
					blocked := usage.Blocked
					usage.mu.Unlock()

					if blocked {
						w.WriteHeader(http.StatusServiceUnavailable)
						w.Write([]byte(`{"error":"bandwidth limit exceeded"}`))
						return
					}
				}
			}

			// Wrap response writer to capture bytes written
			rw := &responseWriter{ResponseWriter: w, host: host, manager: m}
			defer rw.recordDelta()
			next.ServeHTTP(rw, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to track bytes written.
type responseWriter struct {
	http.ResponseWriter
	host         string
	manager      *Manager
	bytesWritten int64
	recorded     int64 // bytes already reported to manager.Record
	wroteHeader  bool
}

// recordDelta reports only the bytes written since the last record. Flush and
// the post-request defer both call it; recording rw.bytesWritten directly each
// time would re-count the whole cumulative total on every flush (a streaming
// response flushed K times would bill ~K× its real size).
func (rw *responseWriter) recordDelta() {
	if delta := rw.bytesWritten - rw.recorded; delta > 0 {
		rw.manager.Record(rw.host, delta)
		rw.recorded = rw.bytesWritten
	}
}

func (rw *responseWriter) Write(p []byte) (n int, err error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err = rw.ResponseWriter.Write(p)
	rw.bytesWritten += int64(n)
	return
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	// Record the bytes produced so far (delta only), then flush downstream so
	// streaming responses (SSE, chunked) actually reach the client.
	rw.recordDelta()
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Handler returns HTTP handlers for bandwidth API.
func (m *Manager) Handler() (allHandler, hostHandler http.HandlerFunc) {
	allHandler = func(w http.ResponseWriter, r *http.Request) {
		statuses := m.GetAllStatus()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statuses)
	}
	hostHandler = func(w http.ResponseWriter, r *http.Request) {
		host := r.PathValue("host")
		status := m.GetStatus(host)
		if status == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"domain not found or bandwidth not enabled"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	}
	return
}
