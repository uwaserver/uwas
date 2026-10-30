package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

// F40300: the canary router queues its stickiness cookie on the response
// before the request is proxied; copying the upstream's headers then replaced
// the whole Set-Cookie list, so any application that sets a cookie of its own
// (session, CSRF) erased the stickiness cookie and the visitor was re-rolled
// between the canary and primary pools on every request.
func canaryServe(t *testing.T, upstreamHeaders string) []string {
	t.Helper()
	addr, _ := rawUpstreamHead(t, upstreamHeaders)
	cfg := config.CanaryConfig{Enabled: true, Weight: 100, Upstreams: []config.Upstream{{Address: addr, Weight: 1}}}
	cr := NewCanaryRouter(cfg, "round_robin", newTestLogger())
	dom := newTestDomain()
	dom.Proxy.Canary = cfg
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	if !cr.Serve(ctx, dom, New(newTestLogger())) {
		t.Fatal("canary did not serve")
	}
	return rec.Header().Values("Set-Cookie")
}

func hasCookie(cookies []string, name string) bool {
	for _, c := range cookies {
		if strings.HasPrefix(c, name+"=") {
			return true
		}
	}
	return false
}

func TestCanaryCookieSurvivesUpstreamSetCookie(t *testing.T) {
	// Control: the upstream sets no cookie, the stickiness cookie is sent.
	if got := canaryServe(t, "X-Up: 1\r\n"); !hasCookie(got, "X-Canary") {
		t.Fatalf("control: X-Canary cookie missing without upstream cookies: %q", got)
	}

	got := canaryServe(t, "Set-Cookie: session=abc; Path=/\r\nSet-Cookie: csrf=def; Path=/\r\n")
	t.Logf("EXPECTED: X-Canary, session and csrf cookies\nACTUAL: %q", got)
	if !hasCookie(got, "X-Canary") {
		t.Errorf("PROBLEM CONFIRMED: canary stickiness cookie erased by the upstream's Set-Cookie: %q", got)
	}
	if !hasCookie(got, "session") || !hasCookie(got, "csrf") {
		t.Errorf("upstream cookies lost: %q", got)
	}
	if len(got) != 3 {
		t.Errorf("got %d Set-Cookie values, want 3: %q", len(got), got)
	}
}
