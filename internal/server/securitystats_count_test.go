package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// The dashboard and Security page show rate_blocked and hotlink_blocked, but
// nothing ever recorded them — the rate-limit 429 and the hotlink 403 skipped
// SecurityStats, so both counters sat at 0 forever. These pin the recording.
func statsFixture(t *testing.T, d config.Domain) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.Root = root
	cfg := &config.Config{
		Global:  config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{d},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s
}

func TestRateLimitBlockIncrementsRateBlocked(t *testing.T) {
	s := statsFixture(t, config.Domain{
		Host: "rl.test", Type: "static", SSL: config.SSLConfig{Mode: "off"},
		Security: config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: 2}},
	})
	h := s.buildMiddlewareChain()

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
		req.Host = "rl.test"
		req.RemoteAddr = "203.0.113.5:1111"
		req.Header.Set("User-Agent", "uwas-test")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := s.securityStats.RateBlocked.Load(); got == 0 {
		t.Error("rate_blocked stayed 0 after rate-limit rejections — counter not recorded")
	}
}

func TestHotlinkBlockIncrementsHotlinkBlocked(t *testing.T) {
	s := statsFixture(t, config.Domain{
		Host: "hl.test", Type: "static", SSL: config.SSLConfig{Mode: "off"},
		Security: config.SecurityConfig{HotlinkProtection: config.HotlinkConfig{
			Enabled:         true,
			AllowedReferers: []string{"hl.test"},
			Extensions:      []string{".jpg"},
		}},
	})
	h := s.buildMiddlewareChain()

	req := httptest.NewRequest(http.MethodGet, "/a.jpg", nil)
	req.Host = "hl.test"
	req.RemoteAddr = "203.0.113.6:2222"
	req.Header.Set("User-Agent", "uwas-test")
	req.Header.Set("Referer", "https://evil.example/") // foreign referer → blocked
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := s.securityStats.HotlinkBlocked.Load(); got == 0 {
		t.Error("hotlink_blocked stayed 0 after a hotlink rejection — counter not recorded")
	}
}
