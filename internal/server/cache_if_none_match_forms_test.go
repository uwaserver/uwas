package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cache hit answers a conditional GET with 304 when If-None-Match matches
// the cached ETag under weak comparison (RFC 9110 §13.1.2): W/ prefixes are
// ignored, several tags may be listed and "*" matches anything.
func TestCacheHitIfNoneMatchForms(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.css"), []byte(strings.Repeat("body{}\n", 300)), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newStaticCacheChain(t, root)
	staticCacheGet(t, h, "/a.css", "identity")
	warm := staticCacheGet(t, h, "/a.css", "identity")
	if warm.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", warm.Header().Get("X-Cache"))
	}
	etag := warm.Header().Get("Etag")
	if etag == "" {
		t.Fatal("cache hit carries no ETag")
	}
	bare := strings.TrimPrefix(etag, "W/")

	for _, tc := range []struct {
		name, inm string
		want      int
	}{
		{"exact", etag, http.StatusNotModified},
		{"strong form", bare, http.StatusNotModified},
		{"weak form", "W/" + bare, http.StatusNotModified},
		{"list", `"other", ` + etag, http.StatusNotModified},
		{"weak list", `W/"other",W/` + bare, http.StatusNotModified},
		{"star", "*", http.StatusNotModified},
		{"different tag", `"other"`, http.StatusOK},
		{"list without match", `"a", W/"b"`, http.StatusOK},
		{"prefix of tag", bare[:len(bare)-1], http.StatusOK},
	} {
		req := httptest.NewRequest("GET", "/a.css", nil)
		req.Host = "cache.test"
		req.Header.Set("User-Agent", "uwas-test")
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("If-None-Match", tc.inm)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: If-None-Match %q -> %d, want %d", tc.name, tc.inm, rec.Code, tc.want)
		}
	}
}
