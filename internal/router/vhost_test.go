package router

import (
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
