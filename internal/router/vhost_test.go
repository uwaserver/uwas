package router

import (
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestVHostExactMatch(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www/a"},
		{Host: "other.com", Root: "/var/www/b"},
	}
	r := NewVHostRouter(domains)

	d := r.Lookup("example.com")
	if d == nil || d.Host != "example.com" {
		t.Errorf("Lookup(example.com) = %v, want example.com", d)
	}

	d = r.Lookup("other.com")
	if d == nil || d.Host != "other.com" {
		t.Errorf("Lookup(other.com) = %v, want other.com", d)
	}
}

func TestVHostAliasMatch(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Aliases: []string{"www.example.com"}, Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	d := r.Lookup("www.example.com")
	if d == nil || d.Host != "example.com" {
		t.Errorf("Lookup(www.example.com) = %v, want example.com", d)
	}
}

func TestVHostWildcardMatch(t *testing.T) {
	domains := []config.Domain{
		{Host: "*.example.com", Root: "/var/www/wildcard"},
		{Host: "specific.example.com", Root: "/var/www/specific"},
	}
	r := NewVHostRouter(domains)

	// Exact match takes priority
	d := r.Lookup("specific.example.com")
	if d == nil || d.Root != "/var/www/specific" {
		t.Errorf("Lookup(specific.example.com) = %v, want /var/www/specific", d)
	}

	// Wildcard match
	d = r.Lookup("anything.example.com")
	if d == nil || d.Root != "/var/www/wildcard" {
		t.Errorf("Lookup(anything.example.com) = %v, want /var/www/wildcard", d)
	}
}

func TestVHostFallback(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www/default"},
	}
	r := NewVHostRouter(domains)

	d := r.Lookup("unknown.com")
	if d == nil || d.Root != "/var/www/default" {
		t.Errorf("Lookup(unknown.com) should fallback to first domain")
	}
}

func TestVHostStripPort(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	d := r.Lookup("example.com:8080")
	if d == nil || d.Host != "example.com" {
		t.Errorf("Lookup with port should strip port and match")
	}
}

func TestVHostCaseInsensitive(t *testing.T) {
	domains := []config.Domain{
		{Host: "Example.COM", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	d := r.Lookup("example.com")
	if d == nil {
		t.Error("Lookup should be case-insensitive")
	}
}

func TestVHostUpdate(t *testing.T) {
	domains := []config.Domain{
		{Host: "old.com", Root: "/var/www/old"},
	}
	r := NewVHostRouter(domains)

	if d := r.Lookup("old.com"); d == nil {
		t.Fatal("old.com should match before update")
	}

	r.Update([]config.Domain{
		{Host: "new.com", Root: "/var/www/new"},
	})

	d := r.Lookup("new.com")
	if d == nil || d.Host != "new.com" {
		t.Error("new.com should match after update")
	}
}

// === Additional coverage tests ===

func TestVHostLookupEmptyHost(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www/default"},
	}
	r := NewVHostRouter(domains)

	// Empty host should fall through to fallback
	d := r.Lookup("")
	if d == nil {
		t.Fatal("Lookup('') should return fallback, not nil")
	}
	if d.Host != "example.com" {
		t.Errorf("Lookup('') = %q, want example.com (fallback)", d.Host)
	}
}

func TestVHostLookupEmptyHostNoFallback(t *testing.T) {
	// No domains at all => no fallback
	r := NewVHostRouter(nil)

	d := r.Lookup("")
	if d != nil {
		t.Errorf("Lookup('') with no domains should return nil, got %v", d)
	}
}

func TestVHostWildcardAlias(t *testing.T) {
	domains := []config.Domain{
		{Host: "main.com", Aliases: []string{"*.main.com"}, Root: "/var/www/main"},
	}
	r := NewVHostRouter(domains)

	d := r.Lookup("sub.main.com")
	if d == nil || d.Host != "main.com" {
		t.Error("wildcard alias should match sub.main.com")
	}
}

func TestVHostLookupPortOnlyHost(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www/default"},
	}
	r := NewVHostRouter(domains)

	// Host string ":8080" should strip port leaving empty, then fallback
	d := r.Lookup(":8080")
	if d == nil {
		t.Fatal("Lookup(':8080') should return fallback")
	}
	if d.Host != "example.com" {
		t.Errorf("Lookup(':8080') = %q, want example.com", d.Host)
	}
}

func TestVHostMultipleWildcardsLongestMatch(t *testing.T) {
	// Multiple wildcards with different suffix lengths to exercise the sort comparator.
	// This covers vhost.go:66-68 (the sort.Slice body).
	domains := []config.Domain{
		{Host: "*.com", Root: "/var/www/short"},
		{Host: "*.example.com", Root: "/var/www/long"},
		{Host: "*.sub.example.com", Root: "/var/www/longest"},
	}
	r := NewVHostRouter(domains)

	// Should match the longest suffix first
	d := r.Lookup("test.sub.example.com")
	if d == nil || d.Root != "/var/www/longest" {
		t.Errorf("expected longest wildcard match, got %v", d)
	}

	d = r.Lookup("test.example.com")
	if d == nil || d.Root != "/var/www/long" {
		t.Errorf("expected long wildcard match, got %v", d)
	}

	d = r.Lookup("random.com")
	if d == nil || d.Root != "/var/www/short" {
		t.Errorf("expected short wildcard match, got %v", d)
	}
}

// --- IsConfigured tests ---

func TestIsConfiguredExactMatch(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www"},
		{Host: "other.com", Root: "/var/www/other"},
	}
	r := NewVHostRouter(domains)

	if !r.IsConfigured("example.com") {
		t.Error("example.com should be configured")
	}
	if !r.IsConfigured("other.com") {
		t.Error("other.com should be configured")
	}
}

func TestIsConfiguredAlias(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Aliases: []string{"www.example.com"}, Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	if !r.IsConfigured("www.example.com") {
		t.Error("www.example.com (alias) should be configured")
	}
}

func TestIsConfiguredWildcard(t *testing.T) {
	domains := []config.Domain{
		{Host: "*.example.com", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	if !r.IsConfigured("sub.example.com") {
		t.Error("sub.example.com should match wildcard")
	}
	if !r.IsConfigured("deep.sub.example.com") {
		t.Error("deep.sub.example.com should match wildcard suffix")
	}
}

func TestIsConfiguredUnknownHost(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	if r.IsConfigured("unknown.com") {
		t.Error("unknown.com should not be configured")
	}
}

func TestIsConfiguredStripsPort(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	if !r.IsConfigured("example.com:443") {
		t.Error("IsConfigured should strip port")
	}
}

func TestIsConfiguredCaseInsensitive(t *testing.T) {
	domains := []config.Domain{
		{Host: "Example.COM", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	if !r.IsConfigured("example.com") {
		t.Error("IsConfigured should be case-insensitive")
	}
	if !r.IsConfigured("EXAMPLE.COM") {
		t.Error("IsConfigured should be case-insensitive")
	}
}

func TestIsConfiguredNoDomains(t *testing.T) {
	r := NewVHostRouter(nil)

	if r.IsConfigured("anything.com") {
		t.Error("should not be configured when no domains are loaded")
	}
}

func TestIsConfiguredWildcardAlias(t *testing.T) {
	domains := []config.Domain{
		{Host: "main.com", Aliases: []string{"*.main.com"}, Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	if !r.IsConfigured("sub.main.com") {
		t.Error("wildcard alias should be configured")
	}
}

// --- Load: host with port registration ---

func TestVHostHostWithPort(t *testing.T) {
	// A domain registered with a port in the Host field
	// should also be findable without the port
	domains := []config.Domain{
		{Host: "example.com:8080", Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	// Lookup without port should match (port-stripped form registered in load)
	d := r.Lookup("example.com")
	if d == nil || d.Root != "/var/www" {
		t.Error("should match host registered with port via port-stripped form")
	}

	// Lookup with the exact port
	d = r.Lookup("example.com:8080")
	if d == nil || d.Root != "/var/www" {
		t.Error("should match host registered with exact port")
	}
}

// --- Load: alias with port registration ---

func TestVHostAliasWithPort(t *testing.T) {
	// An alias registered with a port should also be findable without the port
	domains := []config.Domain{
		{Host: "main.com", Aliases: []string{"alias.com:9090"}, Root: "/var/www"},
	}
	r := NewVHostRouter(domains)

	// Lookup via alias without port (port-stripped form)
	d := r.Lookup("alias.com")
	if d == nil || d.Host != "main.com" {
		t.Error("should match alias registered with port via port-stripped form")
	}

	// Lookup via alias with exact port
	d = r.Lookup("alias.com:9090")
	if d == nil || d.Host != "main.com" {
		t.Error("should match alias with exact port")
	}
}

// --- Concurrent access on VHostRouter ---

func TestVHostConcurrentLookupAndUpdate(t *testing.T) {
	initial := []config.Domain{
		{Host: "initial.com", Root: "/var/www/initial"},
	}
	r := NewVHostRouter(initial)

	var wg sync.WaitGroup

	// Concurrent lookups
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.Lookup("initial.com")
				r.Lookup("unknown.com")
				r.IsConfigured("initial.com")
				r.IsConfigured("unknown.com")
			}
		}()
	}

	// Concurrent updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.Update([]config.Domain{
					{Host: "updated.com", Root: "/var/www/updated"},
				})
			}
		}(i)
	}

	wg.Wait()
	// No data race = pass
}

func TestVHostConcurrentIsConfigured(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Root: "/var/www"},
		{Host: "*.wild.com", Root: "/var/www/wild"},
	}
	r := NewVHostRouter(domains)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.IsConfigured("example.com")
				r.IsConfigured("sub.wild.com")
				r.IsConfigured("unknown.com")
			}
		}()
	}
	wg.Wait()
}

// --- Update edge cases ---

func TestVHostUpdateToEmpty(t *testing.T) {
	r := NewVHostRouter([]config.Domain{
		{Host: "example.com", Root: "/var/www"},
	})
	r.Update(nil)

	d := r.Lookup("example.com")
	if d != nil {
		t.Error("after update to empty, Lookup should return nil")
	}
	if r.IsConfigured("example.com") {
		t.Error("after update to empty, IsConfigured should return false")
	}
}

func TestVHostUpdateReplacesCompletely(t *testing.T) {
	r := NewVHostRouter([]config.Domain{
		{Host: "old.com", Root: "/var/www/old"},
		{Host: "*.old.com", Root: "/var/www/old-wild"},
	})

	r.Update([]config.Domain{
		{Host: "new.com", Root: "/var/www/new"},
	})

	// Old domains gone
	if r.IsConfigured("old.com") {
		t.Error("old.com should no longer be configured")
	}
	if r.IsConfigured("sub.old.com") {
		t.Error("sub.old.com should no longer match wildcard")
	}

	// New domain works
	if !r.IsConfigured("new.com") {
		t.Error("new.com should be configured")
	}
	d := r.Lookup("new.com")
	if d == nil || d.Root != "/var/www/new" {
		t.Error("new.com should resolve correctly")
	}
}

// --- Complex scenario tests ---

func TestVHostComplexRouting(t *testing.T) {
	domains := []config.Domain{
		{Host: "primary.com", Aliases: []string{"www.primary.com", "*.cdn.primary.com"}, Root: "/var/www/primary"},
		{Host: "*.primary.com", Root: "/var/www/primary-wild"},
		{Host: "secondary.com", Root: "/var/www/secondary"},
	}
	r := NewVHostRouter(domains)

	tests := []struct {
		host     string
		wantRoot string
		wantConf bool
	}{
		{"primary.com", "/var/www/primary", true},
		{"www.primary.com", "/var/www/primary", true},              // alias exact match
		{"assets.cdn.primary.com", "/var/www/primary", true},      // alias wildcard
		{"blog.primary.com", "/var/www/primary-wild", true},       // wildcard
		{"secondary.com", "/var/www/secondary", true},
		{"unknown.com", "/var/www/primary", false},                 // fallback
	}

	for _, tt := range tests {
		d := r.Lookup(tt.host)
		if d == nil {
			t.Errorf("Lookup(%q) = nil, want root=%q", tt.host, tt.wantRoot)
			continue
		}
		if d.Root != tt.wantRoot {
			t.Errorf("Lookup(%q).Root = %q, want %q", tt.host, d.Root, tt.wantRoot)
		}
		if got := r.IsConfigured(tt.host); got != tt.wantConf {
			t.Errorf("IsConfigured(%q) = %v, want %v", tt.host, got, tt.wantConf)
		}
	}
}
