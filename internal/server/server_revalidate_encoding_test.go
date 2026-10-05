package server

import (
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/logger"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func refreshEncodedFixture(t *testing.T, encoding string, exists bool) *cache.CachedResponse {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "page.js"), []byte("plaintext"), 0600); err != nil {
		t.Fatal(err)
	}
	if exists {
		if err := os.WriteFile(filepath.Join(root, "page.js.br"), staticBrotliBytes(t, []byte("plaintext")), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig(root)
	cfg.Global.Cache.Enabled = true
	cfg.Global.Cache.MemoryLimit = 1 << 20
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(s.cancel)
	d := &cfg.Domains[0]
	r := httptest.NewRequest("GET", "http://localhost/page.js", nil)
	r.Header.Set("Accept-Encoding", encoding)
	key := s.cache.Key(r)
	s.cache.SetByKey(key, &cache.CachedResponse{StatusCode: 200, Body: []byte("previous"), Created: time.Now(), TTL: time.Hour})
	s.runRevalidate(d, s.staleJobFor(key, r, d, time.Hour, 0))
	entry, _ := s.cache.GetByKey(key)
	return entry
}
func TestRevalidationKeepsEncodedBodiesOutOfPlaintextCache(t *testing.T) {
	for _, tc := range []struct {
		encoding string
		variant  bool
		purge    bool
	}{{"br", true, true}, {"identity", true, false}, {"br", false, false}, {"", true, false}} {
		for n := 0; n < 2; n++ {
			got := refreshEncodedFixture(t, tc.encoding, tc.variant)
			if tc.purge {
				if got != nil {
					t.Fatalf("encoded refresh cached: %+v", got)
				}
			} else if got == nil || string(got.Body) != "plaintext" {
				t.Fatalf("plaintext refresh failed: %+v", got)
			}
		}
	}
	t.Log("FIX VERIFIED")
}
