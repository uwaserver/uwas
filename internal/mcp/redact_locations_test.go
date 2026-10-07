package mcp

// Regression guard for per-location basic-auth redaction in domain_get.
//
// sanitizeDomainForMCP clears the DOMAIN-level BasicAuth.Users, but LocationConfig
// carries its own BasicAuth *BasicAuthConfig ("per-path basic auth",
// internal/config/domain.go). Leaving that untouched leaked the credentials to
// the MCP client — the serialized response contained
// "basic_auth":{"enabled":true,"users":{"ops":"PER-LOCATION-SECRET"}}.
//
// ConfigExport already treats the identical field as a secret
// (internal/admin/settings/handler.go nils both d.BasicAuth.Users and
// d.Locations[j].BasicAuth.Users), and domain_get is documented
// "(secrets redacted)" because its response reaches an AI agent that may sit on
// a different trust boundary than the dashboard.
//
// The second property matters just as much: BasicAuth is a POINTER and Locations
// is a slice, both aliasing the caller's live config. Redaction must not strip
// credentials from the running server, which is why sanitizeLocations copies
// both the slice and the struct — the same discipline sanitizeUpstreams uses.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// domainWithPerLocationAuth builds a config holding both levels of basic auth.
func domainWithPerLocationAuth() config.Domain {
	return config.Domain{
		Host: "shop.example.com",
		BasicAuth: config.BasicAuthConfig{
			Enabled: true,
			Users:   map[string]string{"root": "DOMAIN-LEVEL-SECRET"},
		},
		Locations: []config.LocationConfig{
			{
				Match: "/admin",
				BasicAuth: &config.BasicAuthConfig{
					Enabled: true,
					Realm:   "Admin",
					Users:   map[string]string{"ops": "PER-LOCATION-SECRET"},
				},
			},
			{Match: "/api"}, // no BasicAuth at all — must not panic
		},
	}
}

func lookupDomain(t *testing.T, live config.Domain) config.Domain {
	t.Helper()
	s := New(&config.Config{Domains: []config.Domain{live}}, nil, nil)
	tool, ok := s.tools["domain_get"]
	if !ok {
		t.Fatal("domain_get tool not registered")
	}
	out, err := tool.Handler(json.RawMessage(`{"host":"` + live.Host + `"}`))
	if err != nil {
		t.Fatalf("domain_get: %v", err)
	}
	return out.(config.Domain)
}

// TestDomainGetRedactsPerLocationBasicAuth is the regression guard.
func TestDomainGetRedactsPerLocationBasicAuth(t *testing.T) {
	live := domainWithPerLocationAuth()
	got := lookupDomain(t, live)

	for i := range got.Locations {
		if loc := got.Locations[i].BasicAuth; loc != nil && len(loc.Users) > 0 {
			t.Errorf("Locations[%d].BasicAuth.Users leaked %d credential(s) to the MCP client",
				i, len(loc.Users))
		}
	}

	// Nothing secret anywhere in the serialized payload.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PER-LOCATION-SECRET", "DOMAIN-LEVEL-SECRET"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("domain_get response leaked %q: %s", secret, raw)
		}
	}
}

// TestDomainGetSanitizeDoesNotMutateLiveConfig pins the aliasing guard: the
// caller's config must keep its credentials, because the running server needs
// them to authenticate the very requests these locations protect.
func TestDomainGetSanitizeDoesNotMutateLiveConfig(t *testing.T) {
	live := domainWithPerLocationAuth()
	_ = lookupDomain(t, live)

	if len(live.Locations) != 2 {
		t.Fatalf("live config Locations mutated: got %d", len(live.Locations))
	}
	if live.Locations[0].BasicAuth == nil || live.Locations[0].BasicAuth.Users["ops"] != "PER-LOCATION-SECRET" {
		t.Fatal("sanitizing stripped per-location credentials from the live config")
	}
	if live.BasicAuth.Users["root"] != "DOMAIN-LEVEL-SECRET" {
		t.Fatal("sanitizing stripped domain-level credentials from the live config")
	}
}

// TestDomainGetKeepsNonSecretLocationFields is the control: redaction must not
// over-redact. The location still has to be visible to the agent, including
// that it is protected, so routing decisions stay possible.
func TestDomainGetKeepsNonSecretLocationFields(t *testing.T) {
	got := lookupDomain(t, domainWithPerLocationAuth())

	if len(got.Locations) != 2 {
		t.Fatalf("want 2 locations preserved, got %d", len(got.Locations))
	}
	if got.Locations[0].Match != "/admin" {
		t.Errorf("location Match should survive redaction, got %q", got.Locations[0].Match)
	}
	// Enabled/Realm are not credentials.
	if got.Locations[0].BasicAuth == nil || !got.Locations[0].BasicAuth.Enabled ||
		got.Locations[0].BasicAuth.Realm != "Admin" {
		t.Errorf("non-secret basic-auth metadata should survive: %+v", got.Locations[0].BasicAuth)
	}
}
