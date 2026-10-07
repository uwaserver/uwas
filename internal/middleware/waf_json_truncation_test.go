package middleware

// Regression guard: scanJSONBody must not fail OPEN on bodies the guard
// cannot fully parse.
//
// DomainWAFGuard reads only the first maxBodyScan (64KB) of a request body
// (security.go:232). For Content-Type: application/json those bytes are handed
// to scanJSONBody, which json.Unmarshals them and returned false — "do not
// block" — when the parse failed. Any JSON body larger than the scan window
// arrives truncated mid-structure, so the parse always failed and every
// oversized JSON body skipped the WAF entirely. The non-JSON sibling branch
// (security.go:253-264) has no such dependency and still substring-scans the
// same 64KB, so the exemption was an inconsistency in the JSON path alone.
//
// Contract: wafBodyPatterns are documented at security.go:105-109 as "checked
// against POST body only". A body must never be silently exempt from that
// check because of its size.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// wafTruncationPayload is one of the WAF's own SQL-injection detection
// signatures (wafBodyPatterns: `(?i)(union\s+select|drop\s+table|alter\s+table)`).
// It is a detection string used to trip the rule, not an exploit.
const wafTruncationPayload = "UNION SELECT password FROM users"

// wafTruncationJSON builds a JSON object carrying payload as field "q", padded
// out to at least padTo bytes. The payload is emitted FIRST so it always sits
// inside the maxBodyScan window the guard actually reads; the trailing padding
// is what makes the parsed prefix invalid.
func wafTruncationJSON(payload string, padTo int) string {
	if padTo <= 0 {
		return `{"q":"` + payload + `"}`
	}
	head := `{"q":"` + payload + `","pad":"`
	tail := `"}`
	padLen := padTo - len(head) - len(tail)
	if padLen < 0 {
		padLen = 0
	}
	return head + strings.Repeat("A", padLen) + tail
}

func wafTruncationGuard() func(http.ResponseWriter, *http.Request) bool {
	return DomainWAFGuard(logger.New("error", "text"), nil, nil, nil)
}

func wafTruncationJSONReq(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// An oversized JSON body must still be scanned: the payload is inside the read
// window, and the truncated tail must not exempt the whole request.
func TestWAFScansOversizedJSONBody(t *testing.T) {
	body := wafTruncationJSON(wafTruncationPayload, maxBodyScan+4096)
	if len(body) <= maxBodyScan {
		t.Fatalf("harness bug: body %d bytes is not larger than maxBodyScan %d", len(body), maxBodyScan)
	}
	if !strings.Contains(body[:maxBodyScan], wafTruncationPayload) {
		t.Fatalf("harness bug: payload is not inside the first %d bytes", maxBodyScan)
	}
	rr := httptest.NewRecorder()
	if wafTruncationGuard()(rr, wafTruncationJSONReq(body)) {
		t.Errorf("a %d-byte JSON body carrying %q was allowed: scanJSONBody must not "+
			"treat an unparseable (truncated) prefix as clean", len(body), wafTruncationPayload)
	}
}

// Control: a small, fully-parseable JSON body is still blocked. Pins that the
// structured scan path keeps working after the fallback was added.
func TestWAFFullyParseableJSONBodyStillBlocked(t *testing.T) {
	body := wafTruncationJSON(wafTruncationPayload, 0)
	if len(body) >= maxBodyScan {
		t.Fatalf("harness bug: control body is %d bytes, not small", len(body))
	}
	rr := httptest.NewRecorder()
	if wafTruncationGuard()(rr, wafTruncationJSONReq(body)) {
		t.Errorf("a small JSON body carrying %q was allowed: the WAF is not active here",
			wafTruncationPayload)
	}
}

// Control: a benign oversized JSON body is still allowed. A fix that simply
// blocked every large body would pass the first test and fail this one.
func TestWAFAllowsOversizedBenignJSONBody(t *testing.T) {
	body := wafTruncationJSON("alice@example.com", maxBodyScan+4096)
	rr := httptest.NewRecorder()
	if !wafTruncationGuard()(rr, wafTruncationJSONReq(body)) {
		t.Errorf("a benign %d-byte JSON body was blocked: oversized bodies must be "+
			"scanned, not blanket-denied", len(body))
	}
}

// Control: the JSON fallback must respect security.waf.rules, not bypass it.
// With only the XSS family enabled, a SQLi signature must NOT block.
func TestWAFFallbackRespectsConfiguredFamilies(t *testing.T) {
	guard := DomainWAFGuard(logger.New("error", "text"), nil, []string{WAFXSS}, nil)
	body := wafTruncationJSON(wafTruncationPayload, maxBodyScan+4096)
	rr := httptest.NewRecorder()
	if !guard(rr, wafTruncationJSONReq(body)) {
		t.Errorf("a SQLi signature blocked a WAF configured for XSS only: the " +
			"fallback scan must honour the configured family set")
	}
}
