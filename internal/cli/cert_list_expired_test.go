package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// F55000: an expired certificate must still show how long ago it expired;
// "-" is reserved for certificates with no expiry (off/pending).
func TestCertListShowsDaysLeftForExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
{"host":"old.example","ssl_mode":"auto","status":"expired","issuer":"LE","expiry":"2026-10-01T00:00:00Z","days_left":-28},
{"host":"ok.example","ssl_mode":"auto","status":"active","issuer":"LE","expiry":"2027-01-01T00:00:00Z","days_left":60},
{"host":"new.example","ssl_mode":"auto","status":"pending","issuer":"LE"}]`))
	}))
	defer srv.Close()
	out := captureStdout(t, func() {
		if err := (&CertCommand{}).list([]string{"--api-url", srv.URL, "--api-key", "k"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		last := f[len(f)-1]
		switch f[0] {
		case "old.example":
			if last != "-28" {
				t.Errorf("expired days = %q, want -28 (line %q)", last, line)
			}
		case "ok.example":
			if last != "60" {
				t.Errorf("active days = %q, want 60", last)
			}
		case "new.example":
			if last != "-" {
				t.Errorf("pending days = %q, want -", last)
			}
		}
	}
}
