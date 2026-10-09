package middleware

import (
	"testing"
	"time"
)

// TestGeoCacheKnownCountrySurvivesExpiryAndFailedRefresh guards F445: once an
// IP's country is known, it stays enforced while the refresh is pending or
// dropped, and a failed refresh (worker's set(ip, "")) does not erase it.
// The inflight slot is pre-claimed so lookupCountry never queues a network
// lookup.
func TestGeoCacheKnownCountrySurvivesExpiryAndFailedRefresh(t *testing.T) {
	const ip = "203.0.113.5"
	newCache := func() *geoCache {
		c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
		c.tryClaimInflight(ip)
		return c
	}

	c := newCache()
	c.entries[ip] = geoCacheEntry{country: "CN", expires: time.Now().Add(-time.Second)}
	if got := lookupCountry(ip, nil, c); got != "CN" {
		t.Fatalf("expired entry: got %q, want CN", got)
	}

	c = newCache()
	c.set(ip, "CN")
	c.set(ip, "")
	if got := lookupCountry(ip, nil, c); got != "CN" {
		t.Fatalf("after failed refresh: got %q, want CN", got)
	}
	if ttl := time.Until(c.entries[ip].expires); ttl > 5*time.Minute {
		t.Fatalf("kept entry TTL = %v, want <= 5m so it is retried", ttl)
	}

	c = newCache()
	c.set(ip, "CN")
	c.set(ip, "DE")
	if got := lookupCountry(ip, nil, c); got != "DE" {
		t.Fatalf("successful refresh: got %q, want DE", got)
	}

	c = newCache()
	if got := lookupCountry(ip, nil, c); got != "" {
		t.Fatalf("unknown IP: got %q, want default-allow \"\"", got)
	}
}
