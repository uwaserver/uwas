package config

import (
	"reflect"
	"testing"
)

func TestMergeDomain_ScalarOverride(t *testing.T) {
	existing := Domain{Host: "old.example.com", Type: "static", Root: "/var/www/old"}
	patch := Domain{Host: "new.example.com", Root: "/var/www/new"}

	out := MergeDomain(existing, patch, DomainPatchFields{}, false)

	if out.Host != "new.example.com" {
		t.Errorf("Host: got %q want new.example.com", out.Host)
	}
	if out.Type != "static" {
		t.Errorf("Type: got %q want static (preserved)", out.Type)
	}
	if out.Root != "/var/www/new" {
		t.Errorf("Root: got %q want /var/www/new", out.Root)
	}
}

func TestMergeDomain_AliasesEmptyOnlyClearsWhenKeyPresent(t *testing.T) {
	existing := Domain{Host: "x", Aliases: []string{"a", "b"}}

	// Zero patch + no key: aliases stay.
	got := MergeDomain(existing, Domain{}, DomainPatchFields{HasAliases: false}, false)
	if !reflect.DeepEqual(got.Aliases, []string{"a", "b"}) {
		t.Errorf("aliases should be preserved when key absent, got %v", got.Aliases)
	}

	// Key present but empty: aliases cleared.
	got = MergeDomain(existing, Domain{}, DomainPatchFields{HasAliases: true}, false)
	if len(got.Aliases) != 0 {
		t.Errorf("aliases should clear when key present and empty, got %v", got.Aliases)
	}
}

func TestMergeDomain_PHPSubfieldsIndependent(t *testing.T) {
	existing := Domain{
		Host: "x",
		PHP: PHPConfig{
			FPMAddress: "127.0.0.1:9000",
			IndexFiles: []string{"index.php"},
			MaxUpload:  ByteSize(8 << 20),
			Env:        map[string]string{"FOO": "bar"},
		},
	}
	patch := Domain{
		PHP: PHPConfig{
			IndexFiles: []string{"index.php", "index.html"},
		},
	}

	out := MergeDomain(existing, patch, DomainPatchFields{}, false)
	if out.PHP.FPMAddress != "127.0.0.1:9000" {
		t.Errorf("FPMAddress should be preserved, got %q", out.PHP.FPMAddress)
	}
	if !reflect.DeepEqual(out.PHP.IndexFiles, []string{"index.php", "index.html"}) {
		t.Errorf("IndexFiles should be overridden, got %v", out.PHP.IndexFiles)
	}
	if out.PHP.MaxUpload != ByteSize(8<<20) {
		t.Errorf("MaxUpload should be preserved, got %v", out.PHP.MaxUpload)
	}
	if out.PHP.Env["FOO"] != "bar" {
		t.Errorf("Env should be preserved, got %v", out.PHP.Env)
	}
}

func TestMergeDomain_ReplaceModeAllowsDisablingCache(t *testing.T) {
	existing := Domain{
		Host:  "x",
		Cache: DomainCache{Enabled: true, TTL: 60},
	}
	patch := Domain{
		Cache: DomainCache{Enabled: false}, // explicit disable
	}

	// Merge mode without HasCache: existing cache wins (the dashboard would
	// not have sent a cache field at all).
	out := MergeDomain(existing, patch, DomainPatchFields{HasCache: false}, false)
	if !out.Cache.Enabled {
		t.Errorf("merge mode without HasCache: cache should stay enabled")
	}

	// Replace mode: takes patch verbatim.
	out = MergeDomain(existing, patch, DomainPatchFields{}, true)
	if out.Cache.Enabled {
		t.Errorf("replace mode: cache should be disabled, got %+v", out.Cache)
	}
	if out.Cache.TTL != 0 {
		t.Errorf("replace mode: TTL should be zero, got %d", out.Cache.TTL)
	}
}

func TestMergeDomain_LocationsClearedByEmptyList(t *testing.T) {
	existing := Domain{
		Host:      "x",
		Locations: []LocationConfig{{Match: "/api"}, {Match: "/static"}},
	}
	patch := Domain{Locations: nil}

	// HasLocations=true means "caller sent the key", even if list is empty.
	out := MergeDomain(existing, patch, DomainPatchFields{HasLocations: true}, false)
	if len(out.Locations) != 0 {
		t.Errorf("locations should clear, got %v", out.Locations)
	}
}

func TestMergeDomain_ProxyReplacedAsBlock(t *testing.T) {
	existing := Domain{
		Host: "x",
		Proxy: ProxyConfig{
			Upstreams: []Upstream{{Address: "http://old:8080"}},
		},
	}
	patch := Domain{
		Proxy: ProxyConfig{
			Upstreams: []Upstream{{Address: "http://new1:8080"}, {Address: "http://new2:8080"}},
		},
	}

	out := MergeDomain(existing, patch, DomainPatchFields{}, false)
	if len(out.Proxy.Upstreams) != 2 {
		t.Errorf("proxy block should be replaced wholesale, got %d upstreams", len(out.Proxy.Upstreams))
	}
}

func TestMergeDomain_SSLForcePreservesAndClearsByNestedKey(t *testing.T) {
	existing := Domain{
		Host: "x",
		SSL:  SSLConfig{Mode: "auto", ForceSSL: true, MinVersion: "1.2"},
	}

	out := MergeDomain(existing, Domain{SSL: SSLConfig{Mode: "auto"}}, DomainPatchFields{HasSSL: true}, false)
	if !out.SSL.ForceSSL {
		t.Fatalf("force_ssl should be preserved when ssl.force_ssl key is absent")
	}

	out = MergeDomain(existing, Domain{SSL: SSLConfig{ForceSSL: false}}, DomainPatchFields{HasSSL: true, HasSSLForce: true}, false)
	if out.SSL.ForceSSL {
		t.Fatalf("force_ssl should clear when ssl.force_ssl=false is explicitly sent")
	}
	if out.SSL.Mode != "auto" || out.SSL.MinVersion != "1.2" {
		t.Fatalf("other SSL fields should be preserved, got %+v", out.SSL)
	}
}
