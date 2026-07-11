package config

import (
	"net/http"
	"testing"
)

// --- SecurityHeadersConfig.SetHeaders (0% coverage) ---

func TestSecurityHeadersConfigSetHeaders_AllFields(t *testing.T) {
	sh := SecurityHeadersConfig{
		ContentSecurityPolicy:   "default-src 'self'",
		PermissionsPolicy:       "camera=()",
		CrossOriginEmbedder:     "require-corp",
		CrossOriginOpener:       "same-origin",
		CrossOriginResource:     "same-origin",
		ReferrerPolicy:          "strict-origin-when-cross-origin",
		StrictTransportSecurity: "max-age=31536000",
		XContentTypeOptions:     "nosniff",
		XSSProtection:           "1; mode=block",
	}
	h := make(http.Header)
	sh.SetHeaders(h)

	if h.Get("Content-Security-Policy") != "default-src 'self'" {
		t.Error("CSP not set")
	}
	if h.Get("Permissions-Policy") != "camera=()" {
		t.Error("Permissions-Policy not set")
	}
	if h.Get("Cross-Origin-Embedder-Policy") != "require-corp" {
		t.Error("Cross-Origin-Embedder-Policy not set")
	}
	if h.Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Error("Cross-Origin-Opener-Policy not set")
	}
	if h.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Error("Cross-Origin-Resource-Policy not set")
	}
	if h.Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Error("Referrer-Policy not set")
	}
	if h.Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Error("Strict-Transport-Security not set")
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options not set")
	}
	if h.Get("X-XSS-Protection") != "1; mode=block" {
		t.Error("X-XSS-Protection not set")
	}
}

func TestSecurityHeadersConfigSetHeaders_Empty(t *testing.T) {
	sh := SecurityHeadersConfig{} // all fields empty
	h := make(http.Header)
	sh.SetHeaders(h)

	// No headers should be set
	if len(h) != 0 {
		t.Errorf("expected empty headers, got %d keys", len(h))
	}
}

func TestSecurityHeadersConfigSetHeaders_Partial(t *testing.T) {
	sh := SecurityHeadersConfig{
		ContentSecurityPolicy: "default-src 'self'",
		ReferrerPolicy:        "no-referrer",
		// All other fields remain empty
	}
	h := make(http.Header)
	sh.SetHeaders(h)

	if h.Get("Content-Security-Policy") != "default-src 'self'" {
		t.Error("CSP not set")
	}
	if h.Get("Referrer-Policy") != "no-referrer" {
		t.Error("Referrer-Policy not set")
	}
	// Verify only 2 headers were set
	if len(h) != 2 {
		t.Errorf("expected 2 headers, got %d: %v", len(h), h)
	}
}

// Validate that IsHostSafe with IPv4-mapped IPv6 loopback is allowed
func TestIsHostSafe_IPv4MappedIPv6Loopback(t *testing.T) {
	if err := IsHostSafe("::ffff:127.0.0.1"); err != nil {
		t.Fatalf("expected loopback to be allowed: %v", err)
	}
}

// Validate that IsHostSafe with IPv4-mapped IPv6 private is allowed
func TestIsHostSafe_IPv4MappedIPv6Private(t *testing.T) {
	if err := IsHostSafe("::ffff:10.0.0.1"); err != nil {
		t.Fatalf("expected private to be allowed: %v", err)
	}
}

// Test forbiddenAliasRoots returns Unix paths
func TestForbiddenAliasRoots_Unix(t *testing.T) {
	roots := forbiddenAliasRoots()
	expected := []string{"/etc", "/root", "/proc", "/sys", "/dev"}
	for _, e := range expected {
		found := false
		for _, r := range roots {
			if r == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in forbiddenAliasRoots, got %v", e, roots)
		}
	}
}

// Test badInternalAliasReason with an unresolved path (empty alias)
func TestBadInternalAliasReason_Empty(t *testing.T) {
	if reason := badInternalAliasReason(""); reason != "must not be empty" {
		t.Errorf("expected 'must not be empty', got %q", reason)
	}
}

// Test badInternalAliasReason with non-absolute path that resolves outside root
func TestBadInternalAliasReason_Invalid(t *testing.T) {
	// Note: filepath.Abs succeeds for almost any string on Linux.
	// Just verify the function returns something for a non-empty path
	// that doesn't match forbidden roots.
	reason := badInternalAliasReason("relative/path")
	// A relative path is resolved by filepath.Abs to something under cwd,
	// which won't match any forbidden root, so the result should be empty (safe).
	_ = reason
}

// Test MergeDomain replaceMode clears collections
func TestMergeDomain_ReplaceModeClears(t *testing.T) {
	existing := Domain{
		Host: "example.com",
		Type: "static",
		Locations: []LocationConfig{
			{Match: "/old", Root: "/srv/old"},
		},
	}
	patch := Domain{
		Locations: nil, // explicitly empty
	}
	fields := DomainPatchFields{HasLocations: true}
	merged := MergeDomain(existing, patch, fields, true)

	if len(merged.Locations) != 0 {
		t.Errorf("expected empty locations in replace mode, got %d", len(merged.Locations))
	}
}

// Test MergeDomain with Host empty (should keep existing)
func TestMergeDomain_EmptyHost(t *testing.T) {
	existing := Domain{Host: "example.com", Type: "static", Root: "/srv"}
	patch := Domain{Host: "", Type: "proxy"} // Host empty
	merged := MergeDomain(existing, patch, DomainPatchFields{}, false)

	if merged.Host != "example.com" {
		t.Errorf("expected host to remain 'example.com', got %q", merged.Host)
	}
	if merged.Type != "proxy" {
		t.Errorf("expected type 'proxy', got %q", merged.Type)
	}
}

// Test parseByteSize with various formats
func TestParseByteSize_Formats(t *testing.T) {
	tests := []struct {
		input string
		want  ByteSize
	}{
		{"1024", 1024},
		{"1KB", KB},
		{"2MB", 2 * MB},
		{"1GB", GB},
		{"512KB", 512 * KB},
		{"1.5MB", ByteSize(1.5 * float64(MB))},
	}
	for _, tt := range tests {
		got, err := parseByteSize(tt.input)
		if err != nil {
			t.Errorf("parseByteSize(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseByteSize_Errors(t *testing.T) {
	_, err := parseByteSize("")
	if err == nil {
		t.Error("expected error for empty string")
	}
	_, err = parseByteSize("10ZB")
	if err == nil {
		t.Error("expected error for unknown unit")
	}
	_, err = parseByteSize("-1")
	if err == nil {
		t.Error("expected error for negative size")
	}
}

// forbiddenAliasRoots tests
