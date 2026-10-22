package mcp

import (
	"encoding/json"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// F24300: domain_get must resolve a host the way the admin API's domain
// detail does (case, trailing dot, www. spelling), not byte-for-byte.
func TestDomainGetCanonicalHostLookup(t *testing.T) {
	cfg := &config.Config{Domains: []config.Domain{
		{Host: "example.com", Type: "static", WebhookSecret: "live-secret"},
		{Host: "www.legacy.test", Type: "static"},
		{Host: "other.org", Type: "static"},
	}}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	get := func(host string) (config.Domain, error) {
		in, _ := json.Marshal(map[string]string{"host": host})
		out, err := s.CallTool("domain_get", in)
		if err != nil {
			return config.Domain{}, err
		}
		return out.(config.Domain), nil
	}

	for q, want := range map[string]string{
		"example.com": "example.com", "www.example.com": "example.com", "Example.COM": "example.com",
		"example.com.": "example.com", " example.com ": "example.com", "WWW.EXAMPLE.COM.": "example.com",
		"legacy.test": "www.legacy.test", "www.legacy.test": "www.legacy.test", "other.org": "other.org",
	} {
		d, err := get(q)
		if err != nil || d.Host != want {
			t.Errorf("domain_get(%q) = %q, %v; want %q", q, d.Host, err, want)
		}
	}
	for _, q := range []string{"", "   ", ".", "missing.example.com", "example.com.evil.test", "xexample.com"} {
		if _, err := get(q); err == nil {
			t.Errorf("domain_get(%q) found a domain; want not found", q)
		}
	}

	d, _ := get("WWW.example.com")
	if d.WebhookSecret != "" {
		t.Error("canonical lookup must still redact the webhook secret")
	}
	if cfg.Domains[0].WebhookSecret != "live-secret" {
		t.Error("lookup must not mutate the live config")
	}
}
