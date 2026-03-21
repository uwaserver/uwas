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

func TestVHostCount(t *testing.T) {
	domains := []config.Domain{
		{Host: "a.com", Aliases: []string{"www.a.com"}},
		{Host: "b.com"},
	}
	r := NewVHostRouter(domains)

	// Exact hosts: a.com, www.a.com, b.com = 3
	if c := r.Count(); c != 3 {
		t.Errorf("Count() = %d, want 3", c)
	}
}
