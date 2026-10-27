package static

// F36300: a pre-compressed variant (app.js.gz / app.js.br) was sent without a
// Content-Length. http.ServeContent skips the header for a full response when
// Content-Encoding is already set (it assumes a compressing wrapper), so the
// GET went out chunked with no progress information and a HEAD for the same
// URL carried no length at all, although the file's size is known. nginx's
// gzip_static and Apache both send it.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/router"
)

func precompressedLengthFixture(t *testing.T) (dir, path string, gzSize int) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "app.js")
	stamp := time.Unix(1700000000, 0)
	gz := []byte("GZ-compressed-bytes-0123456789")
	for _, f := range []struct {
		p    string
		body []byte
	}{{path, []byte("original-source-is-longer-than-the-variant-0123456789")}, {path + ".gz", gz}} {
		if err := os.WriteFile(f.p, f.body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f.p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	return dir, path, len(gz)
}

func precompressedLengthServe(h *Handler, dir, path, method string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test/app.js", nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.Serve(&router.RequestContext{Request: r, Response: router.NewResponseWriter(rec), ResolvedPath: path, DocumentRoot: dir})
	return rec
}

func TestPreCompressedResponseCarriesContentLength(t *testing.T) {
	dir, path, gzSize := precompressedLengthFixture(t)
	h := New()
	for _, c := range []struct {
		name, method string
		hdr          map[string]string
		status       int
		wantEncoding string
		wantLength   string // "" = header must be absent
	}{
		{"GET gzip", "GET", map[string]string{"Accept-Encoding": "gzip"}, 200, "gzip", strconv.Itoa(gzSize)},
		{"HEAD gzip", "HEAD", map[string]string{"Accept-Encoding": "gzip"}, 200, "gzip", strconv.Itoa(gzSize)},
		{"range gzip", "GET", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=2-4"}, 206, "gzip", "3"},
		{"If-Range mismatch gzip", "GET", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=2-4", "If-Range": "nope"}, 200, "gzip", strconv.Itoa(gzSize)},
		{"not modified gzip", "GET", map[string]string{"Accept-Encoding": "gzip", "If-Modified-Since": time.Unix(1700000000, 0).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")}, 304, "", ""},
		{"If-Match fails gzip", "GET", map[string]string{"Accept-Encoding": "gzip", "If-Match": `"nope"`}, 412, "gzip", ""},
		{"If-Unmodified-Since fails gzip", "GET", map[string]string{"Accept-Encoding": "gzip", "If-Unmodified-Since": "Mon, 02 Jan 2006 15:04:05 GMT"}, 412, "gzip", ""},
		{"identity control", "GET", nil, 200, "", "53"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := precompressedLengthServe(h, dir, path, c.method, c.hdr)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
			t.Logf("EXPECTED: Content-Length=%q\nACTUAL: Content-Length=%q", c.wantLength, rec.Header().Get("Content-Length"))
			if got := rec.Header().Get("Content-Encoding"); got != c.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, c.wantEncoding)
			}
			if got := rec.Header().Get("Content-Length"); got != c.wantLength {
				t.Fatalf("PROBLEM CONFIRMED: Content-Length = %q, want %q", got, c.wantLength)
			}
		})
	}
}

// Over a real connection the declared length must match the bytes sent, and
// a 412 must not announce a body that never follows.
func TestPreCompressedContentLengthOverTheWire(t *testing.T) {
	dir, path, gzSize := precompressedLengthFixture(t)
	h := New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Serve(&router.RequestContext{Request: r, Response: router.NewResponseWriter(w), ResolvedPath: path, DocumentRoot: dir})
	}))
	defer srv.Close()
	tr := &http.Transport{DisableCompression: true}
	client := &http.Client{Transport: tr}
	for _, c := range []struct {
		name, method string
		hdr          map[string]string
		status, body int
	}{
		{"GET", "GET", nil, 200, gzSize},
		{"HEAD", "HEAD", nil, 200, 0},
		{"If-Match fails", "GET", map[string]string{"If-Match": `"nope"`}, 412, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, srv.URL+"/app.js", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			for k, v := range c.hdr {
				req.Header.Set(k, v)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}
			if resp.StatusCode != c.status || len(body) != c.body {
				t.Fatalf("status=%d body=%d bytes, want %d / %d", resp.StatusCode, len(body), c.status, c.body)
			}
			if c.method == "HEAD" && resp.ContentLength != int64(gzSize) {
				t.Fatalf("HEAD ContentLength = %d, want %d", resp.ContentLength, gzSize)
			}
			if c.method == "GET" && c.status == 200 && resp.ContentLength != int64(gzSize) {
				t.Fatalf("GET ContentLength = %d, want %d", resp.ContentLength, gzSize)
			}
		})
	}
}
