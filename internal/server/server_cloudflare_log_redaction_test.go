package server

// Regression guard: both sinks the Cloudflare-origin guard writes must carry a
// REDACTED path.
//
// rejectNonCloudflareOrigin logged r.URL.RequestURI() verbatim as the "path"
// field, so any secret in the query string of a blocked request went to stdout —
// which on a typical deployment is captured by journald/docker, shipped to a log
// aggregator and retained.
//
// The sibling securityStats.Record call on the line above took the raw URI too.
// That sink is bounded to a 200-entry in-memory ring and only reaches the
// admin-gated dashboard, so the exposure is narrower — but it is the same
// redaction contract at a second site, and a redaction rule that holds at one
// site and not the next is how the first one leaks.
//
// Both now route through middleware.SanitizeURI, the same implementation the
// access log uses, computed once so the two sinks cannot disagree.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/middleware"
)

// captureCloudflareReject builds a server for a cloudflare_only domain, sends
// one request through the full middleware chain, and returns everything the
// logger wrote to stdout plus the dashboard's recent-blocked records.
func captureCloudflareReject(t *testing.T, target string) (string, []middleware.BlockedRequest) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	// logger.New captures the writer at construction.
	log := logger.New("warn", "text")

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "warn", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:     "cf.test",
			Type:     "static",
			Root:     t.TempDir(),
			SSL:      config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{CloudflareOnly: true},
		}},
	}
	s := New(cfg, log)
	s.cancel()
	h := s.buildMiddlewareChain()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "cf.test"
	// botguard 403s empty User-Agents; irrelevant here but keeps the chain
	// behaving the way it does in production.
	req.Header.Set("User-Agent", "regression-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var blocked []middleware.BlockedRequest
	if s.securityStats != nil {
		blocked = s.securityStats.RecentBlocked()
	}

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out, blocked
}

// TestCloudflareRejectLogRedactsSecret is the contract: a blocked
// non-Cloudflare request must not put its secret into the log line.
func TestCloudflareRejectLogRedactsSecret(t *testing.T) {
	const secret = "hunter2supersecret"

	out, _ := captureCloudflareReject(t, "/p?token="+secret)

	if strings.Contains(out, secret) {
		t.Fatalf("secret leaked to the log line: %q", out)
	}
	// The line must still be emitted and must still identify the path.
	if !strings.Contains(out, "blocked non-Cloudflare origin request") {
		t.Fatalf("expected the rejection log line, got %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected the token param to be redacted in the log line, got %q", out)
	}
}

// TestCloudflareRejectRecordRedactsSecret is the same contract for the second
// sink: the dashboard's recent-blocked ring must not carry the secret either.
func TestCloudflareRejectRecordRedactsSecret(t *testing.T) {
	const secret = "hunter2supersecret"

	_, blocked := captureCloudflareReject(t, "/p?token="+secret)

	if len(blocked) == 0 {
		t.Fatal("expected the rejection to be recorded in securityStats")
	}
	entry := blocked[0]
	if strings.Contains(entry.Path, secret) {
		t.Fatalf("secret leaked into the dashboard record: Path=%q", entry.Path)
	}
	if !strings.Contains(entry.Path, "REDACTED") {
		t.Fatalf("expected the token param to be redacted in the dashboard record, got %q", entry.Path)
	}
	// The record must stay useful for diagnosis.
	if !strings.Contains(entry.Path, "/p") {
		t.Fatalf("expected the real path in the dashboard record, got %q", entry.Path)
	}
	if !strings.HasPrefix(entry.Reason, "cloudflare_only") {
		t.Fatalf("expected a cloudflare_only reason, got %q", entry.Reason)
	}
}

// TestCloudflareRejectBothSinksAgree pins that the log line and the dashboard
// record carry the identical string, so the two sinks cannot drift apart again.
func TestCloudflareRejectBothSinksAgree(t *testing.T) {
	out, blocked := captureCloudflareReject(t, "/p?token=x&page=2")

	if len(blocked) == 0 {
		t.Fatal("expected the rejection to be recorded in securityStats")
	}
	rec := blocked[0].Path

	// Text format: path="<value>" — both sinks must quote the same string.
	if !strings.Contains(out, rec) {
		t.Fatalf("sinks disagree:\n  dashboard record: %q\n  log output: %q", rec, out)
	}
}

// TestCloudflareRejectLogRedactsOtherSensitiveParams covers the sibling keys
// isSensitiveQueryParam matches, so the fix is not token-specific.
func TestCloudflareRejectLogRedactsOtherSensitiveParams(t *testing.T) {
	for _, param := range []string{"password", "api_key", "access_token", "secret"} {
		const secret = "LEAKCANARY"
		out, blocked := captureCloudflareReject(t, "/p?"+param+"="+secret)
		if strings.Contains(out, secret) {
			t.Errorf("%s leaked to the log line: %q", param, out)
		}
		for _, b := range blocked {
			if strings.Contains(b.Path, secret) {
				t.Errorf("%s leaked to the dashboard record: %q", param, b.Path)
			}
		}
	}
}

// TestCloudflareRejectLogKeepsNonSensitivePath is the control: redaction must
// not blank out ordinary paths, or both sinks become useless for diagnosis.
func TestCloudflareRejectLogKeepsNonSensitivePath(t *testing.T) {
	out, blocked := captureCloudflareReject(t, "/assets/logo.png?page=2")

	if strings.Contains(out, "REDACTED") {
		t.Errorf("non-sensitive query was redacted in the log line, destroying its diagnostic value: %q", out)
	}
	if !strings.Contains(out, "logo.png") {
		t.Errorf("expected the real path in the log line, got %q", out)
	}
	if len(blocked) == 0 {
		t.Fatal("expected the rejection to be recorded in securityStats")
	}
	if strings.Contains(blocked[0].Path, "REDACTED") {
		t.Errorf("non-sensitive query was redacted in the dashboard record: %q", blocked[0].Path)
	}
	if !strings.Contains(blocked[0].Path, "logo.png") {
		t.Errorf("expected the real path in the dashboard record, got %q", blocked[0].Path)
	}
}
