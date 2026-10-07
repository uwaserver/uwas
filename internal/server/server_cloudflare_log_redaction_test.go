package server

// Regression guard: the Cloudflare-origin guard must log a REDACTED path.
//
// rejectNonCloudflareOrigin logged r.URL.RequestURI() verbatim as the "path"
// field. That line goes to stdout, which on a typical deployment is captured
// by journald/docker, shipped to a log aggregator and retained — so any secret
// in the query string of a blocked request was written to durable, widely-read
// storage. The sibling securityStats.Record call on the line above still takes
// the raw URI, but that value is bounded to an in-memory ring and only ever
// reaches the admin-gated dashboard.
//
// The log now routes through middleware.SanitizeURI, the same implementation
// the access log uses, so the two sinks cannot drift apart again.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// captureCloudflareRejectLogs builds a server for a cloudflare_only domain,
// sends one request through the full middleware chain, and returns everything
// the logger wrote to stdout.
func captureCloudflareRejectLogs(t *testing.T, target string) string {
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

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestCloudflareRejectLogRedactsSecret is the contract: a blocked
// non-Cloudflare request must not put its secret into the log line.
func TestCloudflareRejectLogRedactsSecret(t *testing.T) {
	const secret = "hunter2supersecret"

	out := captureCloudflareRejectLogs(t, "/p?token="+secret)

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

// TestCloudflareRejectLogRedactsOtherSensitiveParams covers the sibling keys
// isSensitiveQueryParam matches, so the fix is not token-specific.
func TestCloudflareRejectLogRedactsOtherSensitiveParams(t *testing.T) {
	for _, param := range []string{"password", "api_key", "access_token", "secret"} {
		const secret = "LEAKCANARY"
		out := captureCloudflareRejectLogs(t, "/p?"+param+"="+secret)
		if strings.Contains(out, secret) {
			t.Errorf("%s leaked to the log line: %q", param, out)
		}
	}
}

// TestCloudflareRejectLogKeepsNonSensitivePath is the control: redaction must
// not blank out ordinary paths, or the log becomes useless for diagnosis.
func TestCloudflareRejectLogKeepsNonSensitivePath(t *testing.T) {
	out := captureCloudflareRejectLogs(t, "/assets/logo.png?page=2")

	if strings.Contains(out, "REDACTED") {
		t.Errorf("non-sensitive query was redacted, destroying the diagnostic value: %q", out)
	}
	if !strings.Contains(out, "logo.png") {
		t.Errorf("expected the real path in the log line, got %q", out)
	}
}
