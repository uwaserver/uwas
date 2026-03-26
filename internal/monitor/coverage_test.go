package monitor

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// TestUpdateDomains covers the UpdateDomains method (line 65-67).
func TestUpdateDomains(t *testing.T) {
	initial := []config.Domain{
		{Host: "a.com", Type: "static"},
	}
	m := New(initial, testLogger())

	if len(m.domains) != 1 {
		t.Fatalf("initial domains = %d, want 1", len(m.domains))
	}

	updated := []config.Domain{
		{Host: "b.com", Type: "static"},
		{Host: "c.com", Type: "php"},
	}
	m.UpdateDomains(updated)

	if len(m.domains) != 2 {
		t.Errorf("updated domains = %d, want 2", len(m.domains))
	}
	if m.domains[0].Host != "b.com" {
		t.Errorf("domains[0].Host = %q, want b.com", m.domains[0].Host)
	}
	if m.domains[1].Host != "c.com" {
		t.Errorf("domains[1].Host = %q, want c.com", m.domains[1].Host)
	}
}

// TestUpdateDomainsEmpty covers setting an empty domain list.
func TestUpdateDomainsEmpty(t *testing.T) {
	initial := []config.Domain{
		{Host: "a.com", Type: "static"},
	}
	m := New(initial, testLogger())

	m.UpdateDomains(nil)

	if len(m.domains) != 0 {
		t.Errorf("domains = %d, want 0 after nil update", len(m.domains))
	}
}
