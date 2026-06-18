package cloudflare

import "testing"

func TestNormalizeCIDRsAcceptsIPsAndCIDRs(t *testing.T) {
	got, err := NormalizeCIDRs([]string{
		"203.0.113.4",
		"203.0.113.0/24",
		"2001:db8::1",
		"2001:db8::/32",
		"203.0.113.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2001:db8::/32", "2001:db8::1/128", "203.0.113.0/24", "203.0.113.4/32"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestIPSetContains(t *testing.T) {
	set := NewIPSet()
	if !set.Contains("203.0.113.44", []string{"203.0.113.0/24"}) {
		t.Fatal("expected IP inside range to match")
	}
	if set.Contains("198.51.100.44", []string{"203.0.113.0/24"}) {
		t.Fatal("expected IP outside range not to match")
	}
}
