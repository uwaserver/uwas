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

	// checkConcurrency bounds how many domains are probed at once. The sweep
	// used to be a plain sequential loop, so one domain that sat out its full
	// 10s timeout delayed every domain after it — with enough sites the real
	// interval drifted far past 30s and the whole board went stale. Bounded
	// rather than unbounded so a few hundred domains cannot open a few hundred
	// sockets at once.
	checkConcurrency = 8

	// failThreshold is how many consecutive bad checks it takes to report a
	// domain as down. A single slow reply used to flip the badge immediately,
	// which is exactly what a site behind a CDN produces now and then: one
	// cold TLS handshake or one bot challenge and a healthy site reads as
	// down. Two in a row is roughly a minute of genuine trouble.
	failThreshold = 2
)

// monitorUserAgent identifies the checker while looking enough like a browser
// to get past the default bot rules on the CDNs that sit in front of these
// sites. A bare "UWAS-Monitor/1.0" was being challenged, and the 403 that came
// back was recorded as the site being unhealthy. The UWAS-Monitor token stays
// in the string: the dispatch path matches on it to keep health checks out of
// the request log, and a probe should still say what it is.
const monitorUserAgent = "Mozilla/5.0 (compatible; UWAS-Monitor/1.0; +https://github.com/uwaserver/uwas)"

// monitorURLSafetyCheck mirrors the indirection used in internal/notify so
// tests against httptest.NewServer (which always binds to 127.0.0.1) can
// disable the SSRF gate. The default policy rejects loopback, private,
// link-local, and cloud-metadata ranges.
var monitorURLSafetyCheck = config.IsWebhookURLSafe

// Monitor periodically checks domain health.
type Monitor struct {
	domainsMu sync.RWMutex
	domains   []config.Domain
	logger    *logger.Logger
	resultsMu sync.RWMutex
	results   map[string]*HealthResult // host -> *HealthResult
	client    *http.Client
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

	// consecutiveFail counts bad checks in a row. Reported status only
	// changes once it reaches failThreshold, so a single slow or challenged
	// reply does not flip a healthy site to down.
	consecutiveFail int
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
		domains: append([]config.Domain(nil), domains...),
		logger:  log,
		results: make(map[string]*HealthResult),
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
	m.domainsMu.Lock()
	m.domains = append([]config.Domain(nil), domains...)
	m.domainsMu.Unlock()
}

func (m *Monitor) snapshotDomains() []config.Domain {
	m.domainsMu.RLock()
	defer m.domainsMu.RUnlock()

	out := make([]config.Domain, len(m.domains))
	copy(out, m.domains)
	return out
}

// Start launches goroutines that check each domain every 30 seconds.
// It blocks until the context is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	// Run an initial check immediately for all domains.
	m.sweep(ctx)

	ticker := time.NewTicker(defaultInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweep(ctx)
		}
	}
}

// sweep checks every domain, up to checkConcurrency at a time, and returns
// once they have all finished. Waiting for the whole sweep keeps two rounds
// from overlapping on a host that is timing out.
func (m *Monitor) sweep(ctx context.Context) {
	domains := m.snapshotDomains()
	if len(domains) == 0 {
		return
	}

	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup
	for _, d := range domains {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(d config.Domain) {
			defer wg.Done()
			defer func() { <-sem }()
			m.checkDomain(ctx, d)
		}(d)
	}
	wg.Wait()
}

func (m *Monitor) checkDomain(ctx context.Context, d config.Domain) {
	scheme := "http"
	if d.SSL.Mode == "auto" || d.SSL.Mode == "manual" {
		scheme = "https"
	}
	url := scheme + "://" + d.Host + "/"

	// Even though domain hosts come from operator-controlled config, refusing
	// to probe loopback / private / cloud-metadata addresses keeps a typo or
	// stale entry (e.g. Host: "169.254.169.254") from turning the monitor
	// into an internal-network scanner.
	if err := monitorURLSafetyCheck(url); err != nil {
		m.logger.Debug("monitor skipping unsafe host", "domain", d.Host, "error", err)
		return
	}

	// Per-check deadline bounded by both checkTimeout and the parent context
	// so a shutdown does not leave HTTP calls hanging in flight.
	reqCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	start := time.Now()
	req, reqErr := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if reqErr != nil {
		return
	}
	req.Header.Set("User-Agent", monitorUserAgent)
	resp, err := m.client.Do(req)
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

	// Load or create result under lock.
	m.resultsMu.Lock()
	result, ok := m.results[d.Host]
	if !ok {
		result = &HealthResult{Host: d.Host}
		m.results[d.Host] = result
	}

	// Append check, keep last maxChecks
	result.Checks = append(result.Checks, check)
	if len(result.Checks) > maxChecks {
		result.Checks = result.Checks[len(result.Checks)-maxChecks:]
	}

	// A bad check has to repeat before it changes what operators see. The
	// check itself is always recorded, so uptime math still counts the blip;
	// only the headline status waits for confirmation.
	if status == "up" {
		result.consecutiveFail = 0
		result.Status = status
	} else {
		result.consecutiveFail++
		if result.consecutiveFail >= failThreshold || result.Status == "" {
			result.Status = status
		}
	}
	result.StatusCode = statusCode
	result.ResponseMs = elapsed
	result.LastCheck = check.Time
	result.Uptime = calculateUptime(result.Checks)
	fails := result.consecutiveFail
	m.resultsMu.Unlock()

	if status != "up" {
		m.logger.Warn("domain health check",
			"domain", d.Host, "status", status, "code", statusCode,
			"response_ms", elapsed, "consecutive_failures", fails,
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

	m.resultsMu.RLock()
	defer m.resultsMu.RUnlock()

	for _, r := range m.results {
		// Return a copy to avoid races
		cp := *r
		cp.Checks = make([]Check, len(r.Checks))
		copy(cp.Checks, r.Checks)
		results = append(results, cp)
	}

	return results
}
