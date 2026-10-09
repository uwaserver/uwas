package mcp

// Regression guard for scheme-less upstream credentials in domain_get.
//
// Proxy upstream and mirror addresses accept "user:pass@host:port" without a
// scheme: config validation and the proxy pool both parse
// config.NormalizeProxyUpstreamAddress(addr), which prefixes http://. Parsed
// raw, url.Parse reads "user" as the scheme and the password as opaque data,
// so u.User was nil and sanitizeURLUserinfo returned the credential unchanged —
// domain_get leaked it despite its "(secrets redacted)" contract.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func TestDomainGetRedactsSchemelessUpstreamUserinfo(t *testing.T) {
	const secret = "SCHEMELESS-UPSTREAM-SECRET"
	cfg := &config.Config{Domains: []config.Domain{{
		Host: "shop.example.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "ops:" + secret + "@10.0.0.5:8080"}},
			Canary:    config.CanaryConfig{Upstreams: []config.Upstream{{Address: "ops:" + secret + "@10.0.0.9:8080"}}},
			Mirror:    config.MirrorConfig{Backend: "ops:" + secret + "@10.0.0.6:8080"},
		},
	}}}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	out, err := s.CallTool("domain_get", json.RawMessage(`{"host":"shop.example.com"}`))
	if err != nil {
		t.Fatalf("domain_get: %v", err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if strings.Contains(got, secret) {
		t.Fatalf("domain_get leaked a scheme-less upstream credential: %s", got)
	}
	for _, host := range []string{"10.0.0.5:8080", "10.0.0.9:8080", "10.0.0.6:8080"} {
		if !strings.Contains(got, host) {
			t.Errorf("redaction dropped upstream host %s: %s", host, got)
		}
	}
	if !strings.Contains(cfg.Domains[0].Proxy.Upstreams[0].Address, secret) {
		t.Error("redaction stripped the credential from the live config")
	}
}

func TestSanitizeUpstreamAddressLeavesPlainAddressesUnchanged(t *testing.T) {
	for _, in := range []string{"", "127.0.0.1:3000", "apps://myapp", "http://10.0.0.1:80/p"} {
		if out := sanitizeUpstreamAddress(in); out != in {
			t.Errorf("sanitizeUpstreamAddress(%q) = %q, want it unchanged", in, out)
		}
	}
}
