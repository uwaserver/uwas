package cache

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// The router resolves "example.com." to the "example.com" domain, so cache
// keys and the implicit site: tag must treat them as one host. Otherwise a
// per-domain purge (SiteTag("example.com")) leaves entries stored under the
// dotted Host behind (F2320).
func TestNormalizeHostTrailingDot(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":       "example.com",
		"Example.COM.":      "example.com",
		"example.com.:8080": "example.com",
		"example.com:8080":  "example.com",
		"[::1]":             "[::1]",
		"[::1]:8080":        "[::1]",
		"":                  "",
	} {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
	if SiteTag("example.com.") != SiteTag("example.com") {
		t.Errorf("SiteTag differs: %q vs %q", SiteTag("example.com."), SiteTag("example.com"))
	}
}

func TestDomainPurgeRemovesDottedHostEntries(t *testing.T) {
	e := NewEngine(context.Background(), 1<<20, "", 0, logger.New("error", "text"))
	hosts := []string{"example.com", "example.com.", "EXAMPLE.COM.:8080"}
	other := httptest.NewRequest("GET", "/p", nil)
	other.Host = "other.test"
	e.Set(other, &CachedResponse{StatusCode: 200, Body: []byte("o"), Created: time.Now(),
		TTL: time.Minute, Tags: []string{SiteTag("other.test")}})

	// Dotted and plain Host share one key: the second Set replaces the first.
	for _, h := range hosts {
		r := httptest.NewRequest("GET", "/p", nil)
		r.Host = h
		e.Set(r, &CachedResponse{StatusCode: 200, Body: []byte("b"), Created: time.Now(),
			TTL: time.Minute, Tags: []string{SiteTag(h)}})
	}
	if n := e.Stats()["entries"]; n != 2 {
		t.Fatalf("entries = %d, want 2 (one per site)", n)
	}

	e.PurgeByTag(SiteTag("example.com"))

	for _, h := range hosts {
		r := httptest.NewRequest("GET", "/p", nil)
		r.Host = h
		if got, status := e.Get(r); got != nil {
			t.Errorf("host %q still cached after domain purge (status %q)", h, status)
		}
	}
	if got, _ := e.Get(other); got == nil {
		t.Error("purge of example.com removed another site's entry")
	}
}
