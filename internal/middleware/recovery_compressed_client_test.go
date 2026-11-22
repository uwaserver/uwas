package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// productionOrderGet sends one request through Recovery, RequestID,
// SecurityHeaders and CompressWith in the server's order.
func productionOrderGet(t *testing.T, h http.Handler, acceptEncoding string) (*http.Response, string, error) {
	t.Helper()
	log := logger.New("error", "text")
	srv := httptest.NewServer(Chain(Recovery(log), RequestID(), SecurityHeaders(), CompressWith(1024, nil))(h))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/page", nil)
	req.Header.Set("Accept-Encoding", acceptEncoding)
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

// F68900: CompressWith closed its writer in a defer, which during a panic
// committed a 200 for a handler that had written nothing; Recovery then saw a
// started response and dropped the connection. Every browser sends
// Accept-Encoding, so a panic before any write must still be answered 500.
func TestRecoveryBeforeWriteReturns500ForCompressingClient(t *testing.T) {
	for _, ae := range []string{"gzip, deflate, br", "gzip", "br"} {
		resp, body, err := productionOrderGet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}), ae)
		if err != nil {
			t.Fatalf("Accept-Encoding %q: request failed: %v (want a 500)", ae, err)
		}
		if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "500 Internal Server Error") {
			t.Fatalf("Accept-Encoding %q: status=%d body=%q, want 500", ae, resp.StatusCode, body)
		}
	}
}

// F68901: the 500 is written on the header map the handler was filling for
// the page, so it carried the page's caching lifetime, validators, cookie and
// body encoding.
func TestRecovery500DropsAbandonedPageHeaders(t *testing.T) {
	page := map[string]string{
		"Cache-Control":       "public, max-age=86400",
		"Set-Cookie":          "sess=abc123; Path=/",
		"ETag":                `"page-v1"`,
		"Last-Modified":       "Mon, 02 Jan 2006 15:04:05 GMT",
		"Content-Encoding":    "gzip",
		"Content-Disposition": `attachment; filename="report.pdf"`,
	}
	for _, ae := range []string{"identity", "gzip"} {
		resp, body, err := productionOrderGet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range page {
				w.Header().Set(k, v)
			}
			panic("boom")
		}), ae)
		if err != nil || resp.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "500 Internal Server Error") {
			t.Fatalf("Accept-Encoding %q: want a 500, got err=%v", ae, err)
		}
		for k := range page {
			if v := resp.Header.Get(k); v != "" {
				t.Errorf("Accept-Encoding %q: 500 carries %s: %s", ae, k, v)
			}
		}
		if resp.Header.Get("X-Request-Id") == "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("Accept-Encoding %q: 500 lost the request id or security headers", ae)
		}
	}
}
