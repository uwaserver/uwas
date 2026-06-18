package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// parseByteSize overflow branch (result >= 1<<63).
func TestParseByteSize_Overflow(t *testing.T) {
	if _, err := parseByteSize("99999999999GB"); err == nil {
		t.Fatal("expected overflow error")
	}
}

// badInternalAliasReason: filesystem root and empty after trim.
func TestBadInternalAliasReason_FilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	if filepath.Separator == '\\' {
		root = `C:\`
	}
	if r := badInternalAliasReason(root); r == "" {
		t.Errorf("filesystem root %q should be rejected", root)
	}
	if r := badInternalAliasReason("   "); r == "" {
		t.Error("whitespace-only alias should be rejected")
	}
}

// forbiddenAliasRoots returns a non-empty list on the current OS.
func TestForbiddenAliasRoots(t *testing.T) {
	if len(forbiddenAliasRoots()) == 0 {
		t.Fatal("expected at least one forbidden root")
	}
}

// Validate: proxy upstream pointing at cloud metadata host is rejected.
func TestValidate_ProxyUpstreamMetadataBlocked(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Type = "proxy"
	cfg.Domains[0].Proxy.Upstreams = []Upstream{
		{Address: "http://169.254.169.254:80"},
	}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("expected metadata block error, got %v", err)
	}
}

// Validate: alias that canonicalizes to empty is skipped (continue branch).
func TestValidate_EmptyAliasSkipped(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Aliases = []string{"", "."}
	if err := Validate(cfg); err != nil {
		t.Fatalf("empty/dot aliases should be skipped, got %v", err)
	}
}

// Validate: invalid canonical_host is reported across the whole-config path.
func TestValidate_InvalidCanonicalHost(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].CanonicalHost = "bogus"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "canonical_host") {
		t.Fatalf("expected canonical_host error, got %v", err)
	}
}

// Validate: valid canonical_host accepted (the case arm).
func TestValidate_ValidCanonicalHost(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].CanonicalHost = "apex"
	if err := Validate(cfg); err != nil {
		t.Fatalf("apex canonical_host should be valid, got %v", err)
	}
}

// mustParseCIDR panics on a bad CIDR.
func TestMustParseCIDR_PanicsOnBad(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for invalid CIDR")
		}
	}()
	_ = mustParseCIDR("not-a-cidr")
}

// stripBOM strips a leading UTF-8 BOM and leaves other data alone.
func TestStripBOM(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello")...)
	if got := stripBOM(withBOM); string(got) != "hello" {
		t.Errorf("BOM not stripped: %q", got)
	}
	if got := stripBOM([]byte("plain")); string(got) != "plain" {
		t.Errorf("non-BOM data altered: %q", got)
	}
	if got := stripBOM([]byte{0xEF}); len(got) != 1 {
		t.Errorf("short data altered: %q", got)
	}
}
