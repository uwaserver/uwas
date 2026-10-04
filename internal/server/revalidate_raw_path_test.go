package server

import (
	"github.com/uwaserver/uwas/internal/config"
	"net/http/httptest"
	"testing"
	"time"
)

func refreshURILocation(t *testing.T, path string) string {
	t.Helper()
	s, _ := ttlFixture(t, 300, 300, nil)
	domain := &config.Domain{Host: "ttl.test", Type: "redirect", Redirect: config.RedirectConfig{Target: "https://target.test", PreservePath: true}}
	req := httptest.NewRequest("GET", "http://ttl.test"+path, nil)
	key := s.cache.Key(req)
	job := s.staleJobFor(key, req, domain, time.Minute, 0)
	s.runRevalidate(domain, job)
	entry, _ := s.cache.GetByKey(key)
	if entry == nil {
		t.Fatal("cache control: redirect not stored")
	}
	return entry.Headers.Get("Location")
}
func TestRevalidationPreservesEncodedRequestURI(t *testing.T) {
	for _, path := range []string{"/one%2Ftwo?q=1", "/one%2ftwo", "/percent%25value", "/with%20space", "/?q=%2F", "/ordinary"} {
		if got := refreshURILocation(t, path); got != "https://target.test"+path {
			t.Fatalf("path=%s got=%s", path, got)
		}
	}
}
