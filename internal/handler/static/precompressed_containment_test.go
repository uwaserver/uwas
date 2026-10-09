package static

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

// A pre-compressed variant is a different file from the one ResolveRequest
// checked. A symlinked app.js.gz pointing outside the doc root must not be
// served in place of app.js; a variant inside the root still is.
func TestPreCompressedVariantMustStayInDocRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	outside := filepath.Join(tmp, "outside")
	for _, d := range []string{filepath.Join(root, "build"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old, newer := time.Unix(1700000000, 0), time.Unix(1700000100, 0)
	write := func(p, body string, ts time.Time) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, ts, ts)
	}
	write(filepath.Join(root, "app.js"), "original", old)
	write(filepath.Join(root, "lib.js"), "lib", old)
	write(filepath.Join(outside, "secret.txt"), "SECRET", newer)
	write(filepath.Join(root, "build", "lib.js.gz"), "BUILT", newer)
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "app.js.gz")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "build", "lib.js.gz"), filepath.Join(root, "lib.js.gz")); err != nil {
		t.Fatal(err)
	}

	domain := &config.Domain{Host: "example.test", Type: "static", Root: root}
	h := New()
	get := func(uri string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://example.test"+uri, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		ctx := &router.RequestContext{Request: r, Response: router.NewResponseWriter(rec)}
		if !ResolveRequest(ctx, domain) {
			t.Fatalf("ResolveRequest(%s) = false", uri)
		}
		h.Serve(ctx)
		return rec
	}

	for i := 0; i < 2; i++ {
		rec := get("/app.js")
		if rec.Body.String() != "original" || rec.Header().Get("Content-Encoding") != "" {
			t.Fatalf("call %d: escaping variant served: enc=%q body=%q", i+1, rec.Header().Get("Content-Encoding"), rec.Body.String())
		}
	}
	if rec := get("/lib.js"); rec.Body.String() != "BUILT" || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("in-root symlinked variant not served: enc=%q body=%q", rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
}
