package server

// Regression guard: the per-domain access log FILE must carry a REDACTED path.
//
// server_dispatch.go passed r.URL.RequestURI() straight into domainLogs.Write.
// domainlog.go has no redaction at all — it appends the string verbatim to an
// operator-configured file (os.OpenFile O_APPEND, 0644, with rotation). So any
// sensitive query param of any request to a domain with access_log configured
// was persisted to disk in storage that outlives the process, readable by any
// local user under the default 0644 mode.
//
// This is the same redaction contract as the access log (accesslog.go) and the
// Cloudflare-origin log line / securityStats record (server.go), now applied to
// the third and fourth sinks. All four route through middleware.SanitizeURI, so
// they cannot drift apart.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// runDomainLogRequest serves one request against a static domain whose
// access_log points at a temp file, then returns the file's contents.
func runDomainLogRequest(t *testing.T, target string) string {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "access.log")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:      "log.test",
			Type:      "static",
			Root:      root,
			SSL:       config.SSLConfig{Mode: "off"},
			AccessLog: config.AccessLogConfig{Path: logPath},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	s.cancel()
	defer s.domainLogs.Close()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "log.test"
	req.Header.Set("User-Agent", "regression-test") // botguard 403s empty UAs
	h := s.buildMiddlewareChain()
	h.ServeHTTP(httptest.NewRecorder(), req)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read domain access log: %v", err)
	}
	return string(data)
}

// TestDomainAccessLogRedactsSecret is the contract: the secret must not reach
// the operator's log file.
func TestDomainAccessLogRedactsSecret(t *testing.T) {
	const secret = "hunter2supersecret"

	out := runDomainLogRequest(t, "/?token="+secret)

	if strings.Contains(out, secret) {
		t.Fatalf("secret leaked into the per-domain access log file: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected the token param to be redacted in the log file, got %q", out)
	}
	// The entry must still be a usable access-log line.
	if !strings.Contains(out, "log.test") && !strings.Contains(out, "/") {
		t.Fatalf("expected a normal access-log line, got %q", out)
	}
}

// TestDomainAccessLogRedactsOtherSensitiveParams covers the sibling keys
// isSensitiveQueryParam matches, so the fix is not token-specific.
func TestDomainAccessLogRedactsOtherSensitiveParams(t *testing.T) {
	for _, param := range []string{"password", "api_key", "access_token", "secret", "signature"} {
		const secret = "LEAKCANARY"
		out := runDomainLogRequest(t, "/?"+param+"="+secret)
		if strings.Contains(out, secret) {
			t.Errorf("%s leaked into the per-domain access log file: %q", param, out)
		}
	}
}

// TestDomainAccessLogKeepsNonSensitiveQuery is the control: ordinary paths must
// survive verbatim, or the access log loses its diagnostic value.
func TestDomainAccessLogKeepsNonSensitiveQuery(t *testing.T) {
	out := runDomainLogRequest(t, "/index.html?page=2&sort=asc")

	if strings.Contains(out, "REDACTED") {
		t.Errorf("non-sensitive query was redacted, destroying the access log's value: %q", out)
	}
	if !strings.Contains(out, "page=2") || !strings.Contains(out, "sort=asc") {
		t.Errorf("expected the real query in the log file, got %q", out)
	}
	if !strings.Contains(out, "index.html") {
		t.Errorf("expected the real path in the log file, got %q", out)
	}
}
