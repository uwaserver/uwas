package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// A trusted non-Cloudflare proxy that appends the client to X-Forwarded-For
// but forwards a client-set CF-Connecting-IP must not let that header win.
func TestRealIPPassthroughCFHeaderNotSpoofable(t *testing.T) {
	var got string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:4000"
	r.Header.Set("CF-Connecting-IP", "6.6.6.6")
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "198.51.100.7:0" {
		t.Errorf("RemoteAddr=%q, want 198.51.100.7:0", got)
	}
}

// Multipart bodies the strict MIME parser rejects, or whose field outgrows the
// scan window, must still be scanned.
func TestDomainWAFMultipartFailClosed(t *testing.T) {
	g := DomainWAFGuard(logger.New("error", "text"), nil, nil, nil)
	payload := "1' UNION SELECT password FROM users--"
	body := func(v string) string {
		return "--XYZ\r\nContent-Disposition: form-data; name=\"q\"\r\n\r\n" + v + "\r\n--XYZ--\r\n"
	}
	cases := map[string]string{
		"multipart/form-data; boundary=XYZ; charset": body(payload),
		"multipart/form-data; boundary=XYZ":          body(payload + strings.Repeat("a", maxBodyScan+1024)),
	}
	for ct, b := range cases {
		r := httptest.NewRequest("POST", "/f.php", bytes.NewReader([]byte(b)))
		r.Header.Set("Content-Type", ct)
		if g(httptest.NewRecorder(), r) {
			t.Errorf("WAF passed multipart payload (ct=%q, len=%d)", ct, len(b))
		}
	}
}
