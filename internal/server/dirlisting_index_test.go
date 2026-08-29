package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// listingServer serves a tree with an index at the root and a subdirectory
// without one, which is the arrangement the precedence rule is about.
func listingServer(t *testing.T, listing bool) *Server {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "HOMEPAGE")
	write("downloads/report.pdf", "pdf")
	write("nested/index.html", "NESTED-INDEX")
	write("nested/other.txt", "other")

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:             "listing.test",
			Root:             dir,
			Type:             "static",
			SSL:              config.SSLConfig{Mode: "off"},
			IndexFiles:       []string{"index.html"},
			DirectoryListing: listing,
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s
}

func get(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "listing.test"
	s.handleRequest(rec, req)
	return rec.Code, rec.Body.String()
}

// TestIndexWinsOverDirectoryListing is the regression. Turning listings on
// used to replace the homepage with a file listing: the directory check ran
// before index resolution, so GET / rendered the doc root instead of
// index.html — losing the front page and publishing every filename in it.
// Apache checks DirectoryIndex before Options +Indexes and nginx checks index
// before autoindex, for the same reason.
func TestIndexWinsOverDirectoryListing(t *testing.T) {
	s := listingServer(t, true)

	code, body := get(t, s, "/")
	if code != 200 {
		t.Fatalf("GET / status = %d, want 200", code)
	}
	if !strings.Contains(body, "HOMEPAGE") {
		t.Errorf("GET / served %.60q, want the index file", body)
	}
	if strings.Contains(body, "Index of /") {
		t.Error("GET / served a directory listing while index.html exists")
	}

	// A nested directory with its own index behaves the same way.
	if _, body := get(t, s, "/nested/"); !strings.Contains(body, "NESTED-INDEX") {
		t.Errorf("GET /nested/ served %.60q, want the nested index", body)
	}
}

// TestDirectoryListingStillWorksWithoutIndex keeps the feature useful: a
// directory with no index is exactly what listings are for.
func TestDirectoryListingStillWorksWithoutIndex(t *testing.T) {
	s := listingServer(t, true)

	code, body := get(t, s, "/downloads/")
	if code != 200 {
		t.Fatalf("GET /downloads/ status = %d, want 200", code)
	}
	if !strings.Contains(body, "report.pdf") {
		t.Errorf("listing did not include the directory's file: %.120q", body)
	}
}

// TestDirectoryWithoutListingIs404 pins the off state: no index, no listing,
// nothing to serve.
func TestDirectoryWithoutListingIs404(t *testing.T) {
	s := listingServer(t, false)

	if code, _ := get(t, s, "/downloads/"); code != 404 {
		t.Errorf("GET /downloads/ status = %d with listings off, want 404", code)
	}
	if _, body := get(t, s, "/"); !strings.Contains(body, "HOMEPAGE") {
		t.Error("GET / stopped serving the index when listings are off")
	}
}

// TestListingResponseCarriesCacheControl covers the side effect the old order
// had: the early return skipped the browser-cache header entirely, so a
// listing — and the homepage it was wrongly replacing — went out with none.
func TestListingResponseCarriesCacheControl(t *testing.T) {
	s := listingServer(t, true)
	s.config.Domains[0].BrowserCache.HTML = config.DefaultBrowserCacheHTML

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "listing.test"
	s.handleRequest(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control on the homepage = %q, want no-cache", got)
	}
}
