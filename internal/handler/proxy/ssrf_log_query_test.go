package proxy

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// "proxy SSRF blocked" must name the backend and the cause but never carry the
// visitor's query string: the access log redacts it, this line must not undo
// that (F2111).
func TestProxySSRFBlockedLogOmitsQuery(t *testing.T) {
	run := func(target, backend string) string {
		var buf bytes.Buffer
		lg := logger.New("error", "text")
		lg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		pool := NewUpstreamPool([]UpstreamConfig{{Address: backend, Weight: 1}})
		req := httptest.NewRequest("GET", target, nil)
		rec := httptest.NewRecorder()
		ctx := router.AcquireContext(rec, req)
		defer router.ReleaseContext(ctx)
		New(lg).Serve(ctx, newTestDomain(), pool, NewBalancer("round_robin"))
		return buf.String()
	}
	const blocked = "http://169.254.169.254"

	// Control: the block is still logged, with backend and cause.
	out := run("/x", blocked)
	if !strings.Contains(out, "proxy SSRF blocked") || !strings.Contains(out, "169.254.169.254") ||
		!strings.Contains(out, "blocked cloud metadata endpoint") {
		t.Fatalf("control: SSRF log lost backend or cause: %q", out)
	}
	for _, target := range []string{"/x?token=SECRETVALUE123", "/a/b?x=1&token=SECRETVALUE123&y=2", "/?token=SECRETVALUE123"} {
		if out := run(target, blocked); strings.Contains(out, "SECRETVALUE123") || strings.Contains(out, "token=") {
			t.Errorf("%s: log leaks the query string: %s", target, strings.TrimSpace(out))
		}
	}
	// Backend credentials in the configured URL stay out of the log too.
	if out := run("/x", "http://user:backendpw@169.254.169.254"); strings.Contains(out, "backendpw") {
		t.Errorf("log leaks backend userinfo: %s", strings.TrimSpace(out))
	}
}
