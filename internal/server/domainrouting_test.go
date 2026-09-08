package server

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A per-domain rate-limit change made through the admin API takes effect via
// onDomainChange → rebuildDomainRouting, not a full reload. Before that path
// rebuilt the rate-limiter map, changing (or disabling) a domain's rate_limit
// in the panel did nothing until a restart: the old limiter kept enforcing.
func TestRebuildDomainRoutingReflectsRateLimitChange(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host: "rl.test", Type: "static", Root: root,
			SSL:      config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: 5}},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	if s.domainRateLimiters["rl.test"] == nil {
		t.Fatal("startup: rl.test should have a rate limiter (requests=5)")
	}

	// Disable: requests=0 must drop the limiter live.
	disabled := []config.Domain{{
		Host: "rl.test", Type: "static", Root: root,
		SSL:      config.SSLConfig{Mode: "off"},
		Security: config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: 0}},
	}}
	s.rebuildDomainRouting(disabled, nil)
	if s.domainRateLimiters["rl.test"] != nil {
		t.Error("after requests=0 the rate limiter must be gone (disabled live)")
	}

	// Re-enable with a new value: the limiter must come back.
	reenabled := []config.Domain{{
		Host: "rl.test", Type: "static", Root: root,
		SSL:      config.SSLConfig{Mode: "off"},
		Security: config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: 3}},
	}}
	s.rebuildDomainRouting(reenabled, nil)
	if s.domainRateLimiters["rl.test"] == nil {
		t.Error("after requests=3 the rate limiter must be rebuilt (enabled live)")
	}
}

// WAF and IP-ACL guards are rebuilt on the same path, so a security change
// through the panel applies live rather than after a restart.
func TestRebuildDomainRoutingReflectsWAFAndIPACL(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Global:  config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{Host: "x.test", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	if s.wafGuards["x.test"] != nil {
		t.Fatal("no WAF guard expected initially")
	}
	on := []config.Domain{{
		Host: "x.test", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"},
		Security: config.SecurityConfig{
			WAF:         config.WAFConfig{Enabled: true},
			IPBlacklist: []string{"203.0.113.9"},
		},
	}}
	s.rebuildDomainRouting(on, nil)
	if s.wafGuards["x.test"] == nil {
		t.Error("WAF guard should be built live after enabling it")
	}
	if s.ipACLGuards["x.test"] == nil {
		t.Error("IP ACL guard should be built live after adding a blacklist")
	}
}
