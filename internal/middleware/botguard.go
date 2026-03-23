package middleware

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Known malicious bot signatures.
var maliciousBots = []string{
	"sqlmap", "nikto", "nmap", "masscan", "zgrab",
	"dirbuster", "gobuster", "wfuzz", "ffuf",
	"nuclei", "httpx", "subfinder",
	"python-requests/", "go-http-client/",
	"curl/", "wget/",
	"scrapy", "ahrefsbot", "semrushbot", "dotbot",
	"mj12bot", "blexbot", "seekport",
	"censys", "shodan", "internetmeasurement",
	"thesis-research", "research-scanner",
}

// Known good bots (search engines).
var goodBots = []string{
	"googlebot", "bingbot", "yandexbot", "baiduspider",
	"duckduckbot", "slurp", "facebot", "twitterbot",
	"linkedinbot", "applebot", "petalbot",
}

// SecurityStats tracks blocked requests for the security dashboard.
type SecurityStats struct {
	WAFBlocked    atomic.Int64
	BotBlocked    atomic.Int64
	RateBlocked   atomic.Int64
	HotlinkBlocked atomic.Int64
	TotalBlocked  atomic.Int64

	// Recent blocked IPs (ring buffer)
	mu          sync.Mutex
	recentIPs   []BlockedRequest
	recentPos   int
	recentFull  bool
}

// BlockedRequest represents a single blocked request.
type BlockedRequest struct {
	Time    time.Time `json:"time"`
	IP      string    `json:"ip"`
	Path    string    `json:"path"`
	Reason  string    `json:"reason"`
	UA      string    `json:"ua,omitempty"`
}

const maxRecentBlocked = 200

// NewSecurityStats creates a new stats tracker.
func NewSecurityStats() *SecurityStats {
	return &SecurityStats{
		recentIPs: make([]BlockedRequest, maxRecentBlocked),
	}
}

// Record adds a blocked request to stats.
func (s *SecurityStats) Record(ip, path, reason, ua string) {
	s.TotalBlocked.Add(1)
	switch reason {
	case "waf":
		s.WAFBlocked.Add(1)
	case "bot":
		s.BotBlocked.Add(1)
	case "rate":
		s.RateBlocked.Add(1)
	case "hotlink":
		s.HotlinkBlocked.Add(1)
	}

	s.mu.Lock()
	s.recentIPs[s.recentPos] = BlockedRequest{
		Time:   time.Now(),
		IP:     ip,
		Path:   path,
		Reason: reason,
		UA:     ua,
	}
	s.recentPos = (s.recentPos + 1) % maxRecentBlocked
	if s.recentPos == 0 {
		s.recentFull = true
	}
	s.mu.Unlock()
}

// Snapshot returns current stats.
func (s *SecurityStats) Snapshot() map[string]any {
	return map[string]any{
		"waf_blocked":     s.WAFBlocked.Load(),
		"bot_blocked":     s.BotBlocked.Load(),
		"rate_blocked":    s.RateBlocked.Load(),
		"hotlink_blocked": s.HotlinkBlocked.Load(),
		"total_blocked":   s.TotalBlocked.Load(),
	}
}

// RecentBlocked returns recent blocked requests.
func (s *SecurityStats) RecentBlocked() []BlockedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []BlockedRequest
	if s.recentFull {
		for i := 0; i < maxRecentBlocked; i++ {
			idx := (s.recentPos + i) % maxRecentBlocked
			if s.recentIPs[idx].IP != "" {
				result = append(result, s.recentIPs[idx])
			}
		}
	} else {
		for i := 0; i < s.recentPos; i++ {
			result = append(result, s.recentIPs[i])
		}
	}
	// Reverse for newest-first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// BotGuard blocks known malicious bots and scanners.
func BotGuard(log *logger.Logger, stats *SecurityStats) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ua := strings.ToLower(r.Header.Get("User-Agent"))

			// Empty UA is suspicious
			if ua == "" {
				stats.Record(r.RemoteAddr, r.URL.Path, "bot", "")
				log.Warn("blocked empty user-agent", "remote", r.RemoteAddr, "path", r.URL.Path)
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}

			// Check malicious bots
			for _, bot := range maliciousBots {
				if strings.Contains(ua, bot) {
					stats.Record(r.RemoteAddr, r.URL.Path, "bot", ua)
					log.Warn("blocked malicious bot", "bot", bot, "remote", r.RemoteAddr, "path", r.URL.Path)
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// IsGoodBot checks if the user-agent is a known search engine bot.
func IsGoodBot(ua string) bool {
	ua = strings.ToLower(ua)
	for _, bot := range goodBots {
		if strings.Contains(ua, bot) {
			return true
		}
	}
	return false
}
