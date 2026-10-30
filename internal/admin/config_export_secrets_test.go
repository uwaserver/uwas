package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// ConfigExport already blanks the global keys, passwords, tokens and webhook
// secrets, but the per-domain webhook secret, the headers a domain adds to the
// request it sends upstream (typically Authorization), the secret path of an
// outbound webhook URL (Slack, Discord, Teams) and credentials embedded in an
// upstream address were exported verbatim. The export is the file operators
// attach to tickets and keep as a backup.
func TestConfigExportMasksDomainAndWebhookSecrets(t *testing.T) {
	secrets := []string{
		"dom-hook-secret-r186",
		"Bearer r186-upstream-token",
		"r186slacktokenpath",
		"r186upstreampass",
		"r186globalhooksecret",
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel: "info", LogFormat: "text",
			Webhooks: []config.WebhookConfig{{
				URL:     "https://hooks.slack.com/services/T000/B000/r186slacktokenpath",
				Secret:  "r186globalhooksecret",
				Enabled: true,
			}},
		},
		Domains: []config.Domain{{
			Host: "app.example.com", Type: "proxy",
			WebhookSecret: "dom-hook-secret-r186",
			Headers:       config.HeadersConfig{RequestAdd: map[string]string{"Authorization": "Bearer r186-upstream-token"}, Add: map[string]string{"X-Frame-Options": "DENY"}},
			Proxy:         config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://svc:r186upstreampass@10.0.0.9:8080", Weight: 1}}},
		}},
	}
	s := testServerFromConfig(t, cfg)
	s.authMgr = newMockAuthManager()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config/export", nil)
	r = r.WithContext(auth.WithUser(r.Context(), &auth.User{ID: "u", Username: "u", Role: auth.RoleAdmin, Enabled: true}))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("PROBLEM CONFIRMED: export contains %q", secret)
		}
	}
	// Controls: what is not a secret stays, so the export is still a usable record.
	for _, keep := range []string{"app.example.com", "hooks.slack.com", "X-Frame-Options", "10.0.0.9:8080"} {
		if !strings.Contains(body, keep) {
			t.Errorf("control: export lost %q:\n%s", keep, body)
		}
	}
	// The live configuration must not be touched by the redaction.
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	d := s.config.Domains[0]
	if d.WebhookSecret == "" || d.Headers.RequestAdd["Authorization"] == "" || !strings.Contains(s.config.Global.Webhooks[0].URL, "r186slacktokenpath") || !strings.Contains(d.Proxy.Upstreams[0].Address, "r186upstreampass") {
		t.Fatal("export redaction modified the live configuration")
	}
}
