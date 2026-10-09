package router

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// An absolute-form Host ("example.com.") must route like the relative form;
// registration already trims the dot.
func TestLookupTrailingDotHost(t *testing.T) {
	r := NewVHostRouter([]config.Domain{
		{Host: "first.com"}, {Host: "example.com"}, {Host: "*.wild.com"},
	})
	for host, want := range map[string]string{
		"example.com.":      "example.com",
		"EXAMPLE.com.:8443": "example.com",
		"www.example.com.":  "example.com",
		"a.wild.com.":       "*.wild.com",
	} {
		d, ok := r.LookupWithStatus(host)
		if !ok || d == nil || d.Host != want {
			t.Errorf("LookupWithStatus(%q) = %v, %v; want %s, true", host, d, ok, want)
		}
	}
	if _, ok := r.LookupWithStatus("example.com.."); ok {
		t.Error(`"example.com.." should not match`)
	}
}

// A port-qualified host must not take over another domain's explicit bare
// host, in either config order, and keeps its own port.
func TestPortQualifiedHostDoesNotClobberExplicitHost(t *testing.T) {
	a := config.Domain{Host: "example.com", Root: "A"}
	b := config.Domain{Host: "example.com:8080", Root: "B"}
	for _, order := range [][]config.Domain{{a, b}, {b, a}} {
		r := NewVHostRouter(order)
		for host, want := range map[string]string{
			"example.com": "A", "example.com:443": "A", "www.example.com": "A", "example.com:8080": "B",
		} {
			if d := r.Lookup(host); d == nil || d.Root != want {
				t.Errorf("order %s: Lookup(%q) = %v; want root %s", order[0].Root, host, d, want)
			}
		}
	}
}
