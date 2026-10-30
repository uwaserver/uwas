package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func newDeepPathServer(t *testing.T, root string) *Server {
	t.Helper()
	cfg := &config.Config{Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{Host: "d.test", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}}}}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); time.Sleep(50 * time.Millisecond) })
	s.handler = s.buildMiddlewareChain()
	return s
}

func deepGet(s *Server, path string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "d.test"
	req.RemoteAddr = "198.51.100.1:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Code
}

// F14400: the .htaccess chain walk visited and cached every prefix of the URL
// path whether or not it existed. A request for N nonexistent segments cost
// O(N^2) and left N cache entries with O(N^2) bytes of keys for good (a 40 KB
// path: 4 s and ~400 MiB). The walk now stops at the first directory that does
// not exist, so it is bounded by the real tree, and nothing is cached for the
// rest.
func TestHtaccessWalkIgnoresNonexistentDeepPath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "x.txt"), []byte("X"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", ".htaccess"), []byte("Require all denied\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "page.txt"), []byte("PAGE"), 0o644)
	s := newDeepPathServer(t, dir)

	if c := deepGet(s, "/sub/x.txt"); c != http.StatusForbidden {
		t.Fatalf("control: /sub/x.txt = %d, want 403", c)
	}
	if c := deepGet(s, "/page.txt"); c != http.StatusOK {
		t.Fatalf("control: /page.txt = %d, want 200", c)
	}
	s.htaccessCacheMu.RLock()
	before := len(s.htaccessCache)
	s.htaccessCacheMu.RUnlock()

	for _, segs := range []int{50, 4000} { // 4000 segments is a path of 8001 bytes, under maxRequestPath
		start := time.Now()
		if c := deepGet(s, "/"+strings.Repeat("a/", segs)+"x.txt"); c != http.StatusNotFound {
			t.Errorf("%d nonexistent segments = %d, want 404", segs, c)
		}
		// Hang detector only: the regression itself is caught by the
		// deterministic cache-entry count below. A wall-clock bound tight
		// enough to matter is not stable (warm ~7ms, but 12s on a loaded CI
		// host under -race).
		if d := time.Since(start); d > 90*time.Second {
			t.Errorf("%d nonexistent segments took %v", segs, d)
		}
	}
	s.htaccessCacheMu.RLock()
	added := len(s.htaccessCache) - before
	s.htaccessCacheMu.RUnlock()
	if added != 0 {
		t.Errorf("a path of nonexistent directories added %d .htaccess cache entries, want 0", added)
	}
}

// The walk must still reach every EXISTING level: a deny several directories
// down a real tree applies, and so does one that has a nonexistent tail.
func TestHtaccessWalkStillAppliesDeepExistingLevels(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	var url strings.Builder
	for i := 0; i < 30; i++ {
		deep = filepath.Join(deep, "d")
		url.WriteString("/d")
	}
	os.MkdirAll(deep, 0o755)
	os.WriteFile(filepath.Join(deep, "secret.txt"), []byte("S"), 0o644)
	os.WriteFile(filepath.Join(deep, "open.txt"), []byte("O"), 0o644)
	os.WriteFile(filepath.Join(deep, ".htaccess"), []byte("<Files secret.txt>\nRequire all denied\n</Files>\n"), 0o644)
	s := newDeepPathServer(t, dir)

	if c := deepGet(s, url.String()+"/secret.txt"); c != http.StatusForbidden {
		t.Errorf("deny 30 levels down: /…/secret.txt = %d, want 403", c)
	}
	if c := deepGet(s, url.String()+"/open.txt"); c != http.StatusOK {
		t.Errorf("/…/open.txt = %d, want 200", c)
	}
	if c := deepGet(s, url.String()+"/missing/secret.txt"); c != http.StatusNotFound {
		t.Errorf("nonexistent tail = %d, want 404", c)
	}
}

func TestForEachExistingDirStopsAtFirstMissing(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "file"), []byte("f"), 0o644)
	for _, tc := range []struct {
		rel  string
		want string
	}{
		{"a/b", "a/b"},
		{"a/b/c/d", "a/b"},
		{"x/y/z", ""},
		{"a/file/under", "a"}, // a file is not a directory; "a" still is
		{".", ""},
	} {
		var got []string
		forEachExistingDir(dir, filepath.FromSlash(tc.rel), func(_, part string) { got = append(got, part) })
		if strings.Join(got, "/") != tc.want {
			t.Errorf("rel %q: visited %v, want %q", tc.rel, got, tc.want)
		}
	}
}
