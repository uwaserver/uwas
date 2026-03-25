package monitor

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

const (
	defaultInterval = 30 * time.Second
	maxChecks       = 100
	checkTimeout    = 10 * time.Second
)

// Monitor periodically checks domain health.
type Monitor struct {
	domains []config.Domain
	logger  *logger.Logger
	results sync.Map // host -> *HealthResult
	client  *http.Client
}

// HealthResult holds the health status of a single domain.
type HealthResult struct {
	Host       string    `json:"host"`
	Status     string    `json:"status"` // "up", "down", "degraded"
	StatusCode int       `json:"status_code"`
	ResponseMs int64     `json:"response_ms"`
	LastCheck  time.Time `json:"last_check"`
	Uptime     float64   `json:"uptime"` // percentage over last 24h
	Checks     []Check   `json:"checks"` // last 100 checks
}

// Check records a single health check result.
type Check struct {
	Time       time.Time `json:"time"`
	StatusCode int       `json:"status_code"`
	ResponseMs int64     `json:"response_ms"`
	Error      string    `json:"error,omitempty"`
}

// New creates a new Monitor for the given domains.
func New(domains []config.Domain, log *logger.Logger) *Monitor {
	return &Monitor{
		domains: domains,
		logger:  log,
		client: &http.Client{
			Timeout: checkTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Follow up to 3 redirects
				if len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// UpdateDomains updates the domain list for health monitoring.
func (m *Monitor) UpdateDomains(domains []config.Domain) {
	m.domains = domains
}

// Start launches goroutines that check each domain every 30 seconds.
// It blocks until the context is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	// Run an initial check immediately for all domains.
	for _, d := range m.domains {
		m.checkDomain(d)
	}

	ticker := time.NewTicker(defaultInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, d := range m.domains {
				m.checkDomain(d)
			}
		}
	}
}

func (m *Monitor) checkDomain(d config.Domain) {
	scheme := "http"
	if d.SSL.Mode == "auto" || d.SSL.Mode == "manual" {
		scheme = "https"
	}
	url := scheme + "://" + d.Host + "/"

	start := time.Now()
	resp, err := m.client.Get(url)
	elapsed := time.Since(start).Milliseconds()

	check := Check{
		Time:       time.Now(),
		ResponseMs: elapsed,
	}

	var statusCode int
	var status string

	if err != nil {
		check.Error = err.Error()
		status = "down"
		statusCode = 0
	} else {
		resp.Body.Close()
		statusCode = resp.StatusCode
		check.StatusCode = statusCode

		if statusCode >= 200 && statusCode < 400 {
			status = "up"
		} else if statusCode >= 400 && statusCode < 500 {
			status = "degraded"
		} else {
			status = "down"
		}
	}

	// Load or create result
	val, _ := m.results.LoadOrStore(d.Host, &HealthResult{
		Host: d.Host,
	})
	result := val.(*HealthResult)

	// Append check, keep last maxChecks
	result.Checks = append(result.Checks, check)
	if len(result.Checks) > maxChecks {
		result.Checks = result.Checks[len(result.Checks)-maxChecks:]
	}

	result.Status = status
	result.StatusCode = statusCode
	result.ResponseMs = elapsed
	result.LastCheck = check.Time
	result.Uptime = calculateUptime(result.Checks)

	m.results.Store(d.Host, result)

	if status != "up" {
		m.logger.Warn("domain health check",
			"host", d.Host, "status", status, "code", statusCode,
			"response_ms", elapsed,
		)
	}
}

// calculateUptime computes uptime percentage over checks within the last 24 hours.
func calculateUptime(checks []Check) float64 {
	cutoff := time.Now().Add(-24 * time.Hour)
	var total, up int
	for _, c := range checks {
		if c.Time.Before(cutoff) {
			continue
		}
		total++
		if c.Error == "" && c.StatusCode >= 200 && c.StatusCode < 400 {
			up++
		}
	}
	if total == 0 {
		return 100.0
	}
	return float64(up) / float64(total) * 100.0
}

// Results returns all domain health results as a slice.
func (m *Monitor) Results() []HealthResult {
	var results []HealthResult
	m.results.Range(func(key, value any) bool {
		r := value.(*HealthResult)
		// Return a copy to avoid races
		cp := *r
		cp.Checks = make([]Check, len(r.Checks))
		copy(cp.Checks, r.Checks)
		results = append(results, cp)
		return true
	})
	return results
}
