package static

import (
	"github.com/uwaserver/uwas/internal/router"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func staticNegotiationVaryFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.js")
	stamp := time.Unix(1700000000, 0)
	for _, f := range []struct{ path, body string }{{p, "original"}, {p + ".gz", "compressed"}} {
		if err := os.WriteFile(f.path, []byte(f.body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f.path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	return p
}
func staticNegotiationVaryServe(h *Handler, p, method, accept, etag, vary string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test/app.js", nil)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	if etag != "" {
		r.Header.Set("If-None-Match", etag)
	}
	rec := httptest.NewRecorder()
	rec.Header().Set("Vary", vary)
	w := router.NewResponseWriter(rec)
	h.Serve(&router.RequestContext{Request: r, Response: w, ResolvedPath: p, DocumentRoot: filepath.Dir(p)})
	return rec
}
func staticNegotiationVaryVary(rec *httptest.ResponseRecorder, token string) bool {
	for _, v := range rec.Result().Header.Values("Vary") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
func TestStaticNegotiationVaryOnIdentityAndEncodedResponses(t *testing.T) {
	p := staticNegotiationVaryFixture(t)
	h := New()
	for _, c := range []struct{ name, method, accept, encoding, body string }{{"identity", "GET", "", "", "original"}, {"refused gzip", "GET", "gzip;q=0", "", "original"}, {"unsupported coding", "GET", "deflate", "", "original"}, {"cached identity", "GET", "", "", "original"}, {"gzip control", "GET", "gzip", "gzip", "compressed"}, {"identity HEAD", "HEAD", "", "", ""}} {
		t.Run(c.name, func(t *testing.T) {
			rec := staticNegotiationVaryServe(h, p, c.method, c.accept, "", "Origin")
			if rec.Code != 200 || rec.Header().Get("Content-Encoding") != c.encoding || rec.Body.String() != c.body {
				t.Fatalf("unexpected response: status=%d encoding=%q body=%q", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
			}
			if !staticNegotiationVaryVary(rec, "Accept-Encoding") || !staticNegotiationVaryVary(rec, "Origin") {
				t.Fatalf("selection metadata missing: %v", rec.Result().Header.Values("Vary"))
			}
		})
	}
	for _, accept := range []string{"", "gzip"} {
		t.Run("conditional "+accept, func(t *testing.T) {
			first := staticNegotiationVaryServe(h, p, "GET", accept, "", "Origin")
			etag := first.Header().Get("ETag")
			if etag == "" {
				t.Fatal("missing etag")
			}
			rec := staticNegotiationVaryServe(h, p, "GET", accept, etag, "Origin")
			if rec.Code != 304 || rec.Body.Len() != 0 || !staticNegotiationVaryVary(rec, "Accept-Encoding") || !staticNegotiationVaryVary(rec, "Origin") {
				t.Fatalf("conditional metadata: status=%d Vary=%v", rec.Code, rec.Result().Header.Values("Vary"))
			}
		})
	}
}
