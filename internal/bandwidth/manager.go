package bandwidth

import (
	"encoding/json"
	"net/http"
	"sync"
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
type DomainUsage struct {
	mu           sync.Mutex
	MonthlyBytes int64     `json:"monthly_bytes"`
	DailyBytes   int64     `json:"daily_bytes"`
	LastReset    time.Time `json:"last_reset"`
	DailyReset   time.Time `json:"daily_reset"`
	LastUpdated  time.Time `json:"last_updated"`
	Blocked      bool      `json:"blocked"`
	Throttled    bool      `json:"throttled"`
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
func (m *Manager) Record(host string, bytes int64) (blocked bool, throttled bool) {
	m.mu.RLock()
	limit, hasLimit := m.limits[host]
	usage, hasUsage := m.usage[host]
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

	// Check if we need to reset counters
	if now.Sub(usage.LastReset) > 30*24*time.Hour {
		usage.MonthlyBytes = 0
		usage.LastReset = now
		usage.Blocked = false
		usage.Throttled = false
	}
	if now.Sub(usage.DailyReset) > 24*time.Hour {
		usage.DailyBytes = 0
		usage.DailyReset = now
		usage.Blocked = false
		usage.Throttled = false
	}

	// Add bytes
	usage.MonthlyBytes += bytes
	usage.DailyBytes += bytes
	usage.LastUpdated = now

	// Check limits
	monthlyLimit := int64(limit.MonthlyLimit)
	dailyLimit := int64(limit.DailyLimit)

	// Send alerts at thresholds (before block/throttle returns)
	if m.alertFn != nil {
		if monthlyLimit > 0 {
			pct := float64(usage.MonthlyBytes) / float64(monthlyLimit)
			if pct >= 0.9 && pct < 0.91 {
				m.alertFn(host, "monthly_90", usage.MonthlyBytes, monthlyLimit)
			} else if pct >= 1.0 && pct < 1.01 {
				m.alertFn(host, "monthly_exceeded", usage.MonthlyBytes, monthlyLimit)
			}
		}
		if dailyLimit > 0 {
			pct := float64(usage.DailyBytes) / float64(dailyLimit)
			if pct >= 0.9 && pct < 0.91 {
				m.alertFn(host, "daily_90", usage.DailyBytes, dailyLimit)
			} else if pct >= 1.0 && pct < 1.01 {
				m.alertFn(host, "daily_exceeded", usage.DailyBytes, dailyLimit)
			}
		}
	}

	// Block if exceeded hard limit
	if limit.Action == "block" {
		if (monthlyLimit > 0 && usage.MonthlyBytes >= monthlyLimit) ||
			(dailyLimit > 0 && usage.DailyBytes >= dailyLimit) {
			usage.Blocked = true
			return true, false
		}
	}

	// Throttle if exceeded threshold (80% for throttle, 100% for block)
	if limit.Action == "throttle" || limit.Action == "" {
		throttleThreshold := int64(float64(monthlyLimit) * 0.8)
		if monthlyLimit > 0 && usage.MonthlyBytes >= throttleThreshold {
			usage.Throttled = true
			return false, true
		}
		throttleThreshold = int64(float64(dailyLimit) * 0.8)
		if dailyLimit > 0 && usage.DailyBytes >= throttleThreshold {
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

	monthlyLimit := int64(limit.MonthlyLimit)
	dailyLimit := int64(limit.DailyLimit)

	status := &Status{
		Host:         host,
		MonthlyBytes: usage.MonthlyBytes,
		DailyBytes:   usage.DailyBytes,
		MonthlyLimit: monthlyLimit,
		DailyLimit:   dailyLimit,
		Blocked:      usage.Blocked,
		Throttled:    usage.Throttled,
		LastReset:    usage.LastReset,
		DailyReset:   usage.DailyReset,
	}

	if monthlyLimit > 0 {
		status.MonthlyPct = float64(usage.MonthlyBytes) / float64(monthlyLimit) * 100
	}
	if dailyLimit > 0 {
		status.DailyPct = float64(usage.DailyBytes) / float64(dailyLimit) * 100
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
	usage.MonthlyBytes = 0
	usage.DailyBytes = 0
	usage.LastReset = time.Now()
	usage.DailyReset = time.Now()
	usage.Blocked = false
	usage.Throttled = false
}

// Middleware returns an HTTP middleware that checks bandwidth limits.
func (m *Manager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host

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
			defer func() {
				if rw.bytesWritten > 0 {
					m.Record(host, rw.bytesWritten)
				}
			}()
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
	wroteHeader  bool
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
	// Record bandwidth after response is complete
	if rw.bytesWritten > 0 {
		rw.manager.Record(rw.host, rw.bytesWritten)
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

// BlockResponse writes a bandwidth exceeded response.
func BlockResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(`{"error":"bandwidth limit exceeded","code":"BANDWIDTH_EXCEEDED"}`))
}

// ThrottleDelay returns the delay duration for throttled requests.
func ThrottleDelay() time.Duration {
	return 500 * time.Millisecond // Add 500ms delay for throttled requests
}
