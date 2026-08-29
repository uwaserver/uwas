package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// purgeTestServer wires a real cache engine holding one entry per host, each
// tagged the way the request path tags them.
func purgeTestServer(t *testing.T, hosts ...string) *Server {
	t.Helper()
	s := testServer()
	s.config.Domains = nil
	for _, h := range hosts {
		s.config.Domains = append(s.config.Domains, config.Domain{
			Host:  h,
			Type:  "static",
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 300},
		})
	}

	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, logger.New("error", "text"))
	for _, h := range hosts {
		eng.SetByKey("GET|http|"+h+"|/page|", &cache.CachedResponse{
			StatusCode: 200,
			Body:       []byte("body"),
			Created:    time.Now(),
			TTL:        5 * time.Minute,
			Tags:       []string{cache.SiteTag(h)},
		})
	}
	s.SetCache(eng)
	return s
}

func postPurge(t *testing.T, s *Server, body map[string]string) (int, CachePurgeResponse) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/cache/purge", bytes.NewReader(raw)))

	var out CachePurgeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, out
}

// TestCachePurgeByHost is the API half of the per-domain purge fix: the caller
// sends a host and the server resolves the tag, rather than the caller
// synthesizing `site:<host>` against a scheme it cannot see.
func TestCachePurgeByHost(t *testing.T) {
	s := purgeTestServer(t, "one.test", "two.test")

	code, resp := postPurge(t, s, map[string]string{"host": "one.test"})
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Count != 1 {
		t.Errorf("count = %d, want 1", resp.Count)
	}
	if resp.Host != "one.test" {
		t.Errorf("host = %q, want one.test", resp.Host)
	}
	if got := s.cache.Stats()["entries"]; got != 1 {
		t.Errorf("entries left = %d, want 1 (the other domain must survive)", got)
	}
}

// TestCachePurgeByHostNormalizes covers casing and an explicit port.
func TestCachePurgeByHostNormalizes(t *testing.T) {
	s := purgeTestServer(t, "one.test")

	_, resp := postPurge(t, s, map[string]string{"host": "ONE.test:8443"})
	if resp.Count != 1 {
		t.Errorf("count = %d, want 1 for an uppercase host with a port", resp.Count)
	}
}

// TestCachePurgeReportsZeroCount is the guard against the failure mode that
// hid this bug: count carried omitempty, so a purge that matched nothing
// serialized identically to one that worked.
func TestCachePurgeReportsZeroCount(t *testing.T) {
	s := purgeTestServer(t, "one.test")

	rec := httptest.NewRecorder()
	raw := []byte(`{"host":"absent.test"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/cache/purge", bytes.NewReader(raw)))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	count, present := body["count"]
	if !present {
		t.Fatalf("count missing from %s — a no-op purge must not look like a successful one", rec.Body.String())
	}
	if count != float64(0) {
		t.Errorf("count = %v, want 0", count)
	}
}

// TestCachePurgeByTagStillWorks keeps the pre-existing tag API honest.
func TestCachePurgeByTagStillWorks(t *testing.T) {
	s := purgeTestServer(t, "one.test")

	_, resp := postPurge(t, s, map[string]string{"tag": cache.SiteTag("one.test")})
	if resp.Count != 1 {
		t.Errorf("count = %d, want 1", resp.Count)
	}
}
