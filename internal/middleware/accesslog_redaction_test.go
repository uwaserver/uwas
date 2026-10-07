package middleware

// Regression guard: sanitizeURI must fail CLOSED, never open.
//
// r.URL.Query() discards url.ParseQuery's error and silently skips any pair it
// cannot unescape. A sensitive param carrying a malformed escape (token=abc%zz)
// therefore vanished from the parsed map: the redaction loop never saw it,
// needsRedaction stayed false, and the raw URI — secret included — was written
// to the access log. A redaction control that fails open is worse than one that
// does not exist, because the log is exactly where secrets are assumed to be
// safe.
//
// redactReferer in the same file already fails closed on a parse error, so this
// also pins the two redaction paths to the same policy.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeURIFailsClosedOnMalformedEscape(t *testing.T) {
	cases := []struct {
		name   string
		uri    string
		secret string
	}{
		{"token bad escape", "/p?token=abc%zz", "abc%zz"},
		{"token truncated escape", "/p?token=abc%", "abc%"},
		{"api_key bad escape", "/p?api_key=s3cret%GG", "s3cret%GG"},
		{"password bad escape", "/p?password=hunter2%z", "hunter2%z"},
		{"access_token bad escape", "/p?access_token=zz%y", "zz%y"},
		{"secret alongside valid param", "/p?page=3&token=abc%zz", "abc%zz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://x.test"+tc.uri, nil)
			got := sanitizeURI(r)

			if strings.Contains(got, tc.secret) {
				t.Fatalf("secret leaked into the access log: got %q", got)
			}
			// A parse failure must redact the whole query rather than guess.
			if !strings.Contains(got, "REDACTED") {
				t.Fatalf("expected the query to be redacted on parse failure, got %q", got)
			}
		})
	}
}

// TestSanitizeURIWellFormedStillRedacts keeps the normal path honest: a
// well-formed sensitive param is redacted and its siblings survive, so a
// blanket "always redact everything" fix would fail here.
func TestSanitizeURIWellFormedStillRedacts(t *testing.T) {
	r := httptest.NewRequest("GET", "http://x.test/p?token=abc&page=3", nil)
	got := sanitizeURI(r)
	if strings.Contains(got, "abc") {
		t.Errorf("token must be redacted, got %q", got)
	}
	if !strings.Contains(got, "page=3") {
		t.Errorf("non-sensitive param must survive, got %q", got)
	}
}

// TestSanitizeURINonSensitiveUntouched pins that ordinary queries are logged
// verbatim — the fix must not redact logs wholesale and destroy their value.
func TestSanitizeURINonSensitiveUntouched(t *testing.T) {
	r := httptest.NewRequest("GET", "http://x.test/p?page=2&sort=asc", nil)
	got := sanitizeURI(r)
	if !strings.Contains(got, "page=2") || !strings.Contains(got, "sort=asc") {
		t.Errorf("non-sensitive query must pass through, got %q", got)
	}
	if strings.Contains(got, "REDACTED") {
		t.Errorf("unexpected redaction of a non-sensitive query: %q", got)
	}
}

// TestRedactAndSanitizeAgreeOnParseFailure pins the two redaction paths in
// this file to one policy: a query that fails to parse is redacted by both.
func TestRedactAndSanitizeAgreeOnParseFailure(t *testing.T) {
	const malformed = "token=abc%zz"

	fromURI := sanitizeURI(httptest.NewRequest("GET", "http://x.test/p?"+malformed, nil))
	if strings.Contains(fromURI, "abc") {
		t.Errorf("sanitizeURI leaked on parse failure: %q", fromURI)
	}

	fromReferer := redactReferer("http://example.com/p?" + malformed)
	if strings.Contains(fromReferer, "abc") {
		t.Errorf("redactReferer leaked on parse failure: %q", fromReferer)
	}
}
