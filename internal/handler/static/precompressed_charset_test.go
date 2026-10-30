package static

// F55301: the identity response of a text file gets "; charset=utf-8" appended
// when its (operator-supplied) MIME type lacks one, but the pre-compressed
// variant overwrote Content-Type with the bare MIME lookup, so the same URL
// declared a charset or not depending on Accept-Encoding. A browser reading a
// gzip'd .json/.js without a charset falls back to its own guess.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreCompressedKeepsCharsetOfIdentityResponse(t *testing.T) {
	stamp := time.Unix(1700000000, 0)
	h := &Handler{mime: NewMIMERegistry(map[string]string{
		".css": "text/css", ".js": "application/javascript", ".json": "application/json",
		".svg": "image/svg+xml", ".png": "image/png",
	})}
	for _, c := range []struct{ file, wantType string }{
		{"app.css", "text/css; charset=utf-8"},
		{"app.js", "application/javascript; charset=utf-8"},
		{"data.json", "application/json; charset=utf-8"},
		{"logo.svg", "image/svg+xml; charset=utf-8"},
		{"image.png", "image/png"}, // control: a binary type gets no charset either way
	} {
		t.Run(c.file, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, c.file)
			for _, f := range []string{path, path + ".gz"} {
				if err := os.WriteFile(f, []byte("x-0123456789"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(f, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			identity := precompressedLengthServe(h, dir, path, "GET", nil).Header().Get("Content-Type")
			gz := precompressedLengthServe(h, dir, path, "GET", map[string]string{"Accept-Encoding": "gzip"})
			if gz.Header().Get("Content-Encoding") != "gzip" {
				t.Fatalf("gzip variant not served")
			}
			t.Logf("EXPECTED: %q (identity %q)\nACTUAL: %q", c.wantType, identity, gz.Header().Get("Content-Type"))
			if identity != c.wantType {
				t.Fatalf("identity Content-Type = %q, want %q", identity, c.wantType)
			}
			if got := gz.Header().Get("Content-Type"); got != identity {
				t.Fatalf("pre-compressed Content-Type = %q, identity answers %q", got, identity)
			}
		})
	}
}
