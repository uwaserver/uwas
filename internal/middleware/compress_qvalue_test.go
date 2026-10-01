package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: encoding negotiation must honor an Accept-Encoding q=0 refusal.
//
// selectEncoding used to strip the ";q=..." suffix off each token and then
// only ask "is this token br or gzip?", never reading the quality value it had
// just discarded. A client sending "gzip;q=0" — an explicit "do not send me
// gzip" per RFC 9110 §12.5.3 — was served a gzip body anyway.
//
// The static handler already enforced this rule for pre-compressed files
// (internal/handler/static/handler.go, acceptsEncoding); these tests pin the
// same contract on the dynamic compression middleware.

// compressNegotiated runs the production Compress middleware over a
// compressible text/plain body and returns the Content-Encoding the client
// actually received ("" = uncompressed).
func compressNegotiated(acceptEncoding string) string {
	body := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 200)
	h := Compress(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	r := httptest.NewRequest(http.MethodGet, "http://example.test/page", nil)
	r.Header.Set("Accept-Encoding", acceptEncoding)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Header().Get("Content-Encoding")
}

// --- The contract ---------------------------------------------------------

func TestCompressHonorsAcceptEncodingQZeroRefusal(t *testing.T) {
	refused := []string{"gzip;q=0", "gzip;q=0.0", "br;q=0", "br;q=0, gzip;q=0"}
	for _, accept := range refused {
		if got := compressNegotiated(accept); got != "" {
			t.Errorf("Accept-Encoding %q explicitly refuses every coding, but the "+
				"response was compressed with Content-Encoding %q", accept, got)
		}
	}
}

// A refusal of the preferred coding must fall through to an acceptable one,
// not to no compression at all.
func TestCompressQZeroFallsThroughToAcceptableCoding(t *testing.T) {
	cases := []struct {
		accept string
		want   string
	}{
		{"br;q=0, gzip", "gzip"},
		{"gzip;q=0, br", "br"},
	}
	for _, c := range cases {
		if got := compressNegotiated(c.accept); got != c.want {
			t.Errorf("Accept-Encoding %q should be served as %q, got %q",
				c.accept, c.want, got)
		}
	}
}

// The negotiation unit itself, so a regression localizes precisely.
// q=1.0 and sub-1.0 positive q-values must still be honored as requests.
func TestSelectEncodingQualityValueZero(t *testing.T) {
	cases := []struct {
		accept string
		want   encodingType
	}{
		// Explicit refusals.
		{"gzip;q=0", encodingNone},
		{"gzip;q=0.0", encodingNone},
		{"br;q=0", encodingNone},
		{"br;q=0, gzip;q=0", encodingNone},
		// Refusal of the preferred coding falls through.
		{"br;q=0, gzip", encodingGzip},
		{"gzip;q=0, br", encodingBrotli},
		// Positive q-values remain full-strength requests.
		{"gzip;q=1.0", encodingGzip},
		{"br;q=0.9, gzip;q=0.1", encodingBrotli},
		{"gzip;q=1.0, br;q=0", encodingGzip},
		// Plain tokens, unaffected by the fix.
		{"br", encodingBrotli},
		{"gzip", encodingGzip},
		{"br, gzip", encodingBrotli},
		{"gzip, br", encodingBrotli},
		{"", encodingNone},
		{"identity", encodingNone},
		{"deflate", encodingNone},
	}
	for _, c := range cases {
		if got := selectEncoding(c.accept); got != c.want {
			t.Errorf("selectEncoding(%q) = %d, want %d", c.accept, got, c.want)
		}
	}
}

// Coding names are case-insensitive, and a whitespace-padded q= is still a
// refusal.
func TestSelectEncodingQZeroTokenFormatting(t *testing.T) {
	refused := []string{
		"GZIP;q=0", // coding name is case-insensitive
		"gzip; q=0",
		"gzip;q=0 ; deflate",
		"br;q=0.000",
	}
	for _, accept := range refused {
		if got := selectEncoding(accept); got != encodingNone {
			t.Errorf("selectEncoding(%q) = %d, want encodingNone — q=0 is a refusal",
				accept, got)
		}
	}
	if got := selectEncoding("GZIP"); got != encodingGzip {
		t.Errorf("selectEncoding(\"GZIP\") = %d, want encodingGzip", got)
	}
}
