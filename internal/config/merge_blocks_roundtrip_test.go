package config

import "testing"

// F47000/F47001: MergeDomain stores a patch block as a whole replacement
// whenever the patch carries the key. The dashboard forms depend on two
// consequences, pinned here so a change is a conscious one:
//
//   - a block that is sent back unchanged survives (the form must send every
//     field, including the ones it does not show);
//   - a disabled basic_auth block may keep its users and realm, because
//     ValidateBasicAuth accepts it, so "disabled" does not have to mean "erased";
//   - a compression block with the default values replaces a stored custom one,
//     which is how an edit resets compression (an absent key changes nothing).

func TestMergeDomainDisabledBasicAuthKeepsUsersWhenSentBack(t *testing.T) {
	existing := Domain{Host: "a.example", Type: "static"}
	existing.BasicAuth = BasicAuthConfig{Enabled: false, Realm: "Staff", Users: map[string]string{"alice": "pw"}}
	fields := DomainPatchFields{HasBasicAuth: true}

	// The old form sent only {enabled:false}: the stored users are replaced.
	patch := Domain{BasicAuth: BasicAuthConfig{Enabled: false}}
	merged := MergeDomain(existing, patch, fields, false)
	if len(merged.BasicAuth.Users) != 0 || merged.BasicAuth.Realm != "" {
		t.Fatalf("expected {enabled:false} to replace the block, got %+v", merged.BasicAuth)
	}

	// Sending the saved users and realm back keeps them, and the result is valid.
	patch = Domain{BasicAuth: existing.BasicAuth}
	merged = MergeDomain(existing, patch, fields, false)
	if merged.BasicAuth.Realm != "Staff" || merged.BasicAuth.Users["alice"] != "pw" {
		t.Fatalf("a round-tripped disabled block must be preserved, got %+v", merged.BasicAuth)
	}
	if err := ValidateBasicAuth("domain", merged.BasicAuth); err != nil {
		t.Fatalf("a disabled block that lists users must validate: %v", err)
	}

	// Control: without the key the stored block is untouched.
	merged = MergeDomain(existing, Domain{}, DomainPatchFields{}, false)
	if merged.BasicAuth.Users["alice"] != "pw" {
		t.Fatalf("a patch without basic_auth must not touch it, got %+v", merged.BasicAuth)
	}
}

func TestMergeDomainDefaultCompressionBlockResetsStoredCustomBlock(t *testing.T) {
	existing := Domain{Host: "a.example", Type: "static"}
	existing.Compression = CompressionConfig{Enabled: BoolPtr(true), MinSize: 4096, Types: []string{"text/html"}}

	// What the edit form sends once the operator sets the defaults again.
	patch := Domain{Compression: CompressionConfig{Enabled: BoolPtr(true), MinSize: 1024}}
	merged := MergeDomain(existing, patch, DomainPatchFields{HasCompression: true}, false)
	if merged.Compression.MinSize != 1024 || len(merged.Compression.Types) != 0 {
		t.Fatalf("expected the default block to replace the stored one, got %+v", merged.Compression)
	}

	// Control: a patch without the key leaves the stored block alone, which is
	// why the form has to send one when a saved block exists.
	merged = MergeDomain(existing, Domain{}, DomainPatchFields{}, false)
	if merged.Compression.MinSize != 4096 || len(merged.Compression.Types) != 1 {
		t.Fatalf("a patch without compression must not touch it, got %+v", merged.Compression)
	}
}

func TestMergeDomainReplacesWholeSecurityCacheAndResourceBlocks(t *testing.T) {
	existing := Domain{Host: "a.example", Type: "static"}
	existing.Security = SecurityConfig{CloudflareOnly: true, BlockedPaths: []string{"/x"}, GeoBlockCountries: []string{"XX"}}
	existing.Cache = DomainCache{Enabled: true, TTL: 60, Tags: []string{"t"}, ESI: true}
	existing.Resources = ResourceLimits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100}
	fields := DomainPatchFields{HasSecurity: true, HasCache: true, HasResources: true}

	patch := Domain{}
	patch.Security.BlockedPaths = []string{"/y"}
	patch.Cache = DomainCache{Enabled: true, TTL: 120}
	patch.Resources = ResourceLimits{CPUPercent: 10}
	merged := MergeDomain(existing, patch, fields, false)
	if merged.Security.CloudflareOnly || len(merged.Security.GeoBlockCountries) != 0 {
		t.Fatalf("expected the whole security block to be replaced, got %+v", merged.Security)
	}
	if len(merged.Cache.Tags) != 0 || merged.Cache.ESI {
		t.Fatalf("expected the whole cache block to be replaced, got %+v", merged.Cache)
	}
	if merged.Resources.MemoryMB != 0 || merged.Resources.PIDMax != 0 {
		t.Fatalf("expected the whole resources block to be replaced, got %+v", merged.Resources)
	}

	// Round-tripped blocks survive.
	patch = Domain{Security: existing.Security, Cache: existing.Cache, Resources: existing.Resources}
	merged = MergeDomain(existing, patch, fields, false)
	if !merged.Security.CloudflareOnly || !merged.Cache.ESI || merged.Resources.PIDMax != 100 {
		t.Fatalf("round-tripped blocks must be preserved, got %+v", merged)
	}
}
