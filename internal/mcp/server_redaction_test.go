package mcp

// Regression cover for the domain_get credential leak: sanitizeDomainForMCP
// clears env / basic-auth / webhook / key secrets, but URL-shaped fields carry
// HTTP basic-auth userinfo in their authority, and that userinfo used to reach
// the MCP response verbatim. MCP output crosses to a lower-trust AI-agent
// boundary, so the sanitizer is the only thing standing between a proxy
// upstream password and that agent.
//
// Every value below is a fabricated placeholder, not a real credential.
// example.com is IANA-reserved for documentation; 10.0.0.x is RFC1918.
// URLs are assembled with net/url at runtime so no credential-shaped string
// appears in this file.

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

const (
	regressionUser = "fixture-service-account"

	regressionUpstreamPass = "UPSTREAMPLACEHOLDER"
	regressionCanaryPass   = "CANARYPLACEHOLDER"
	regressionMirrorPass   = "MIRRORPLACEHOLDER"
	regressionRedirectPass = "REDIRECTPLACEHOLDER"

	regressionPHPEnv  = "PHPENVPLACEHOLDER"
	regressionAppEnv  = "APPENVPLACEHOLDER"
	regressionBasic   = "BASICAUTHPLACEHOLDER"
	regressionWebhook = "WEBHOOKPLACEHOLDER"
	regressionKeyPath = "/etc/uwas/ssl/privkey.fixture"
)

func regressionUserinfoURL(scheme, pass, host string) string {
	u := &url.URL{Scheme: scheme, Host: host, Path: "/"}
	u.User = url.UserPassword(regressionUser, pass)
	return u.String()
}

func regressionDomain() config.Domain {
	return config.Domain{
		Host: "example.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{
				{Address: regressionUserinfoURL("http", regressionUpstreamPass, "10.0.0.5:8080"), Weight: 1},
			},
			Canary: config.CanaryConfig{
				Upstreams: []config.Upstream{
					{Address: regressionUserinfoURL("http", regressionCanaryPass, "10.0.0.9:8080"), Weight: 1},
				},
			},
			Mirror: config.MirrorConfig{
				Backend: regressionUserinfoURL("http", regressionMirrorPass, "10.0.0.7:8080"),
			},
		},
		Redirect: config.RedirectConfig{
			Target: regressionUserinfoURL("https", regressionRedirectPass, "go.example.com"),
		},
		PHP: config.PHPConfig{
			Env: map[string]string{"SAMPLE_SETTING": regressionPHPEnv},
		},
		App: config.AppConfig{
			Env: map[string]string{"SAMPLE_SETTING": regressionAppEnv},
		},
		BasicAuth: config.BasicAuthConfig{
			Users: map[string]string{"operator": regressionBasic},
		},
		SSL:           config.SSLConfig{Key: regressionKeyPath},
		WebhookSecret: regressionWebhook,
	}
}

func callDomainGetJSON(t *testing.T, d config.Domain) string {
	t.Helper()
	cfg := &config.Config{Domains: []config.Domain{d}}
	srv := New(cfg, logger.New("error", "text"), metrics.New())
	res, err := srv.CallTool("domain_get", json.RawMessage(`{"host":"example.com"}`))
	if err != nil {
		t.Fatalf("domain_get returned an error: %v", err)
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal domain_get result: %v", err)
	}
	return string(out)
}

// The defect: userinfo on every URL-shaped field must not reach MCP output.
func TestDomainGetDoesNotLeakURLUserinfoCredentials(t *testing.T) {
	got := callDomainGetJSON(t, regressionDomain())

	leaks := []struct{ token, where string }{
		{regressionUpstreamPass, "proxy upstream address"},
		{regressionCanaryPass, "canary upstream address"},
		{regressionMirrorPass, "mirror backend"},
		{regressionRedirectPass, "redirect target"},
	}
	for _, l := range leaks {
		if strings.Contains(got, l.token) {
			t.Errorf("domain_get leaked the %s userinfo %q into MCP output; "+
				"MCP responses cross to a lower-trust AI-agent boundary. Got: %s",
				l.where, l.token, got)
		}
	}
}

// Control: the five fields the function documents that it clears stay cleared.
func TestDomainGetStillRedactsDocumentedSecretFields(t *testing.T) {
	got := callDomainGetJSON(t, regressionDomain())
	for _, s := range []string{
		regressionPHPEnv, regressionAppEnv, regressionBasic,
		regressionWebhook, regressionKeyPath,
	} {
		if strings.Contains(got, s) {
			t.Errorf("expected %q to be redacted, but it appears in %s", s, got)
		}
	}
}

// Control: non-secret configuration must still be returned. Guards against a
// "fix" that blanks the whole domain and silently breaks the tool.
func TestDomainGetStillReturnsNonSecretConfig(t *testing.T) {
	got := callDomainGetJSON(t, regressionDomain())
	if !strings.Contains(got, "example.com") {
		t.Errorf("expected the domain host in output, got %s", got)
	}
	if !strings.Contains(got, "10.0.0.5") {
		t.Errorf("expected the non-secret upstream host to survive, got %s", got)
	}
}

// TestSanitizeUpstreamsDoesNotMutateCaller guards the aliasing trap: a slice
// header copied by value still shares its backing array, so sanitizing in place
// would strip credentials from the caller's live config and break the running
// proxy that needs them to reach its backend.
func TestSanitizeUpstreamsDoesNotMutateCaller(t *testing.T) {
	live := []config.Upstream{{Address: regressionUserinfoURL("http", regressionMirrorPass, "10.0.0.7:8080")}}
	original := live[0].Address

	got := sanitizeUpstreams(live)

	if live[0].Address != original {
		t.Errorf("sanitizeUpstreams mutated the caller's slice: got %q, want %q",
			live[0].Address, original)
	}
	if strings.Contains(got[0].Address, regressionMirrorPass) {
		t.Errorf("sanitizeUpstreams left the credential in the copy: %q", got[0].Address)
	}
	if !strings.Contains(got[0].Address, "10.0.0.7") {
		t.Errorf("sanitizeUpstreams dropped the host: %q", got[0].Address)
	}
}

// TestSanitizeURLUserinfoLeavesOrdinaryValuesUnchanged covers the boundaries the
// redaction must not touch: empty, unparseable, and credential-free values.
func TestSanitizeURLUserinfoLeavesOrdinaryValuesUnchanged(t *testing.T) {
	cases := []string{
		"",
		"http://127.0.0.1:3000",
		"https://api.example.com/v1",
		"apps://my-app",
		"not a url at all",
		"http://[::1]:8080",
	}
	for _, in := range cases {
		if out := sanitizeURLUserinfo(in); out != in {
			t.Errorf("sanitizeURLUserinfo(%q) = %q, want it unchanged", in, out)
		}
	}
}
