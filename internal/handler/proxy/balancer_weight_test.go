package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u
}

// weightSelectCounts runs n selections through a balancer built the way
// server_routing.go builds one, and tallies the hits per backend host.
func weightSelectCounts(t *testing.T, algorithm string, upstreams []UpstreamConfig, n int) map[string]int {
	t.Helper()
	backends := NewUpstreamPool(upstreams).Healthy()
	if len(backends) == 0 {
		t.Fatal("no healthy backends")
	}
	b := NewBalancer(algorithm)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	counts := make(map[string]int, len(backends))
	for i := 0; i < n; i++ {
		got := b.Select(backends, req)
		if got == nil {
			t.Fatalf("Select returned nil on iteration %d", i)
		}
		counts[got.URL.Host]++
	}
	return counts
}

// TestRoundRobinHonoursUpstreamWeight pins the documented contract for
// `upstreams[].weight`.
//
// SPECIFICATION.md documents round_robin as "Distribute in order, based on
// weight" and README.md's canonical proxy example ships weight: 3 / weight: 1.
// The weight reached Backend (and was validated by config as operator-tunable)
// but no selection path ever read it, so every backend was served an equal
// share and the operator's capacity plan was silently discarded.
func TestRoundRobinHonoursUpstreamWeight(t *testing.T) {
	counts := weightSelectCounts(t, "round_robin", []UpstreamConfig{
		{Address: "http://10.0.0.1:3000", Weight: 3},
		{Address: "http://10.0.0.2:3001", Weight: 1},
	}, 400)

	// 300/100 expected. A weight-ignoring router lands on an exact 200/200,
	// so this band separates the two behaviours unambiguously.
	const heavy, light = "10.0.0.1:3000", "10.0.0.2:3001"
	if counts[heavy] < 260 || counts[heavy] > 340 {
		t.Errorf("weight:3 backend got %d/400 requests, want ~300: %v", counts[heavy], counts)
	}
	if counts[light] < 60 || counts[light] > 140 {
		t.Errorf("weight:1 backend got %d/400 requests, want ~100: %v", counts[light], counts)
	}
}

// TestRoundRobinEqualWeightsBalanced is the control: equal weights must still
// split evenly. It also pins the unweighted path for anyone who configures a
// pool without weights.
func TestRoundRobinEqualWeightsBalanced(t *testing.T) {
	counts := weightSelectCounts(t, "round_robin", []UpstreamConfig{
		{Address: "http://10.0.0.1:3000", Weight: 1},
		{Address: "http://10.0.0.2:3001", Weight: 1},
		{Address: "http://10.0.0.3:3002", Weight: 1},
	}, 300)

	for host, c := range counts {
		if c < 80 || c > 120 {
			t.Errorf("equal weights gave %s %d/300, want ~100: %v", host, c, counts)
		}
	}
	if len(counts) != 3 {
		t.Errorf("expected all 3 backends to receive traffic, got %v", counts)
	}
}

// TestRoundRobinWeightZeroTreatedAsOne covers the boundary the fix introduces:
// a backend whose weight is absent (0) must behave like weight 1, not be
// starved or dropped. Omitting `weight:` is the common case, since the field
// is `omitempty`.
func TestRoundRobinWeightZeroTreatedAsOne(t *testing.T) {
	counts := weightSelectCounts(t, "round_robin", []UpstreamConfig{
		{Address: "http://10.0.0.1:3000", Weight: 0}, // no weight configured
		{Address: "http://10.0.0.2:3001", Weight: 1},
	}, 200)

	for host, c := range counts {
		if c < 80 || c > 120 {
			t.Errorf("weight 0 was not treated as 1: %s got %d/200, want ~100: %v", host, c, counts)
		}
	}
	if len(counts) != 2 {
		t.Errorf("a weight-0 backend was dropped from rotation: %v", counts)
	}
}

// TestRoundRobinSkipsTotalZeroWeights covers the defensive guard: if every
// weight is non-positive the balancer must still rotate over all backends
// rather than returning nil (which would surface as a 502 for the domain).
func TestRoundRobinSkipsTotalZeroWeights(t *testing.T) {
	backends := []*Backend{
		{URL: mustParseURL(t, "http://10.0.0.1:3000")},
		{URL: mustParseURL(t, "http://10.0.0.2:3001")},
	}
	rr := &RoundRobin{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		got := rr.Select(backends, req)
		if got == nil {
			t.Fatal("Select returned nil for a non-empty pool")
		}
		seen[got.URL.Host] = true
	}
	if len(seen) != 2 {
		t.Errorf("zero-weight backends were not rotated over: saw %v", seen)
	}
}

// TestWeightedAlgorithmNameStillBalances checks the `algorithm: weighted`
// spelling, which config validates as legal and NewBalancer maps to
// RoundRobin, keeps distributing by weight.
func TestWeightedAlgorithmNameStillBalances(t *testing.T) {
	counts := weightSelectCounts(t, "weighted", []UpstreamConfig{
		{Address: "http://10.0.0.1:3000", Weight: 3},
		{Address: "http://10.0.0.2:3001", Weight: 1},
	}, 400)

	if counts["10.0.0.1:3000"] < 260 || counts["10.0.0.1:3000"] > 340 {
		t.Errorf("algorithm=weighted ignored weight: %v", counts)
	}
}
