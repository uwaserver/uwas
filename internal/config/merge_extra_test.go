package config

import "testing"

// TestMergeDomain_AllScalarAndBlockFields exercises the many independently
// gated branches of MergeDomain that the original merge_test.go did not reach:
// IP, Root, App field-by-field merge, Resources, Htaccess, Redirect, Security,
// Compression, and the HasCanonical presence key.
func TestMergeDomain_AllScalarAndBlockFields(t *testing.T) {
	existing := Domain{
		Host: "old.com",
		Type: "static",
		IP:   "1.1.1.1",
		Root: "/old/root",
		App: AppConfig{
			Command: "old-cmd",
			Runtime: "node",
			Port:    3000,
			WorkDir: "/old/work",
			Env:     map[string]string{"A": "1"},
		},
	}
	patch := Domain{
		Host: "new.com",
		Type: "proxy",
		IP:   "2.2.2.2",
		Root: "/new/root",
		App: AppConfig{
			Command: "new-cmd",
			Runtime: "python",
			Port:    4000,
			WorkDir: "/new/work",
			Env:     map[string]string{"B": "2"},
		},
		Resources: ResourceLimits{CPUPercent: 50, MemoryMB: 256, PIDMax: 10},
		Htaccess:  HtaccessConfig{Mode: "full"},
		Redirect:  RedirectConfig{Target: "https://x"},
	}
	patch.Security.RateLimit.Requests = 99
	patch.Compression.Enabled = true
	patch.Compression.Algorithms = []string{"gzip"}

	fields := DomainPatchFields{
		HasCanonical: true,
		HasSecurity:  true,
	}
	patch.CanonicalHost = "apex"

	got := MergeDomain(existing, patch, fields, false)

	if got.Host != "new.com" || got.Type != "proxy" || got.IP != "2.2.2.2" || got.Root != "/new/root" {
		t.Errorf("scalar overrides wrong: %+v", got)
	}
	if got.CanonicalHost != "apex" {
		t.Errorf("HasCanonical override failed: %q", got.CanonicalHost)
	}
	if got.App.Command != "new-cmd" || got.App.Runtime != "python" || got.App.Port != 4000 || got.App.WorkDir != "/new/work" {
		t.Errorf("app fields not merged: %+v", got.App)
	}
	if got.App.Env["B"] != "2" {
		t.Errorf("app env not merged: %+v", got.App.Env)
	}
	if got.Resources.CPUPercent != 50 || got.Resources.MemoryMB != 256 || got.Resources.PIDMax != 10 {
		t.Errorf("resources not merged: %+v", got.Resources)
	}
	if got.Htaccess.Mode != "full" {
		t.Errorf("htaccess not merged: %+v", got.Htaccess)
	}
	if got.Redirect.Target != "https://x" {
		t.Errorf("redirect not merged: %+v", got.Redirect)
	}
	if got.Security.RateLimit.Requests != 99 {
		t.Errorf("security not merged: %+v", got.Security)
	}
	if !got.Compression.Enabled {
		t.Errorf("compression not merged: %+v", got.Compression)
	}
}

// Legacy SSL path: fields.HasSSL is false but patch carries an SSL block, so
// the zero-check fallback applies (mode + force + cert/key/min_version).
func TestMergeDomain_LegacySSLPath(t *testing.T) {
	existing := Domain{Host: "x.com", Type: "static"}
	patch := Domain{Host: "x.com", Type: "static"}
	patch.SSL.Mode = "manual"
	patch.SSL.ForceSSL = true
	patch.SSL.Cert = "/c.pem"
	patch.SSL.Key = "/k.pem"
	patch.SSL.MinVersion = "1.2"

	got := MergeDomain(existing, patch, DomainPatchFields{}, false)
	if got.SSL.Mode != "manual" || !got.SSL.ForceSSL || got.SSL.Cert != "/c.pem" ||
		got.SSL.Key != "/k.pem" || got.SSL.MinVersion != "1.2" {
		t.Fatalf("legacy SSL merge failed: %+v", got.SSL)
	}
}

// Replace mode: bool app fields, locations/basicauth/cache/security/compression
// are taken verbatim from patch.
func TestMergeDomain_ReplaceModeFullBlocks(t *testing.T) {
	existing := Domain{
		Host: "x.com", Type: "static",
		App:       AppConfig{AutoRestart: true, Disabled: false},
		Locations: []LocationConfig{{Match: "/old"}},
	}
	existing.Cache.TTL = 100
	patch := Domain{Host: "x.com", Type: "static"}
	patch.App.AutoRestart = false
	patch.App.Disabled = true

	got := MergeDomain(existing, patch, DomainPatchFields{}, true)
	if got.App.AutoRestart != false || got.App.Disabled != true {
		t.Errorf("replace-mode bool app fields wrong: %+v", got.App)
	}
	if got.Locations != nil {
		t.Errorf("replace-mode should clear locations, got %+v", got.Locations)
	}
	if got.Cache.TTL != 0 {
		t.Errorf("replace-mode should replace cache verbatim, got TTL=%d", got.Cache.TTL)
	}
}

// Proxy block replaced wholesale when patch has upstreams; HasSSL true with
// only mode set exercises the presence-keyed SSL branch independently.
func TestMergeDomain_HasSSLPresenceKeyed(t *testing.T) {
	existing := Domain{Host: "x.com", Type: "static"}
	existing.SSL.Mode = "auto"
	existing.SSL.Cert = "/keep.pem"
	patch := Domain{Host: "x.com", Type: "static"}
	// HasSSL true, Mode empty -> keep existing mode; cert empty -> keep existing.
	got := MergeDomain(existing, patch, DomainPatchFields{HasSSL: true}, false)
	if got.SSL.Mode != "auto" || got.SSL.Cert != "/keep.pem" {
		t.Fatalf("HasSSL presence-keyed merge wrong: %+v", got.SSL)
	}
}
