package server

import (
	"github.com/uwaserver/uwas/internal/cache"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func cachedHeaderValuesHeaders(t *testing.T, values []string, names ...string) []string {
	t.Helper()
	s, h := ttlFixture(t, 300, 300, nil)
	req := httptest.NewRequest("GET", "http://ttl.test/index.html", nil)
	req.Host = "ttl.test"
	name := "Link"
	if len(names) > 0 {
		name = names[0]
	}
	s.cache.Set(req, &cache.CachedResponse{StatusCode: 200, Headers: http.Header{name: values}, Body: []byte("cached"), TTL: 300 * time.Second, Created: time.Now()})
	rec := doRequest(h, "/index.html")
	if rec.Header().Get("X-Cache") != cache.StatusHit || rec.Body.String() != "cached" {
		t.Fatal("cache seam control failed", rec.Header(), rec.Body.String())
	}
	return rec.Header().Values("Link")
}
func TestCachedResponseRetainsHeaderValues(t *testing.T) {
	for _, want := range [][]string{{"one"}, {"one", "two"}, {"one", "one", "three"}} {
		for _, name := range []string{"Link", "link", "LINK"} {
			if got := cachedHeaderValuesHeaders(t, want, name); !reflect.DeepEqual(got, want) {
				t.Fatalf("header=%s got=%v want=%v", name, got, want)
			}
		}
	}
}
