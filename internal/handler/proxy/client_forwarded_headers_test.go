package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

// UWAS writes authoritative X-Forwarded-For/Proto/Host/X-Real-IP, but backends
// such as Spring's ForwardedHeaderFilter prefer Forwarded and the other
// X-Forwarded-* variants. A client-supplied copy must not reach the upstream
// on either the HTTP or the WebSocket path.
var clientForwardedSpoof = map[string]string{
	"Forwarded":          "for=6.6.6.6;proto=https;host=evil.example",
	"X-Forwarded-Port":   "443",
	"X-Forwarded-Prefix": "/evil",
	"X-Forwarded-Server": "evil.example",
	"X-Forwarded-Ssl":    "on",
	"X-Forwarded-Scheme": "https",
}

func clientForwardedHTTPSeen(t *testing.T, spoof bool) http.Header {
	t.Helper()
	var got http.Header
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer up.Close()
	pool := NewUpstreamPool([]UpstreamConfig{{Address: up.URL, Weight: 1}})
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("X-Control", "keep")
	if spoof {
		for k, v := range clientForwardedSpoof {
			req.Header.Set(k, v)
		}
		// Named by Connection: also hop-by-hop, must not be re-laundered.
		req.Header.Add("Forwarded", "for=7.7.7.7")
	}
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	New(newTestLogger()).Serve(ctx, newTestDomain(), pool, NewBalancer("round_robin"))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	return got
}

func TestProxyDropsClientForwardedHeaders(t *testing.T) {
	c := clientForwardedHTTPSeen(t, false)
	if c.Get("X-Forwarded-For") != "1.2.3.4" || c.Get("X-Control") != "keep" {
		t.Fatalf("control: XFF=%q X-Control=%q", c.Get("X-Forwarded-For"), c.Get("X-Control"))
	}
	g := clientForwardedHTTPSeen(t, true)
	for k := range clientForwardedSpoof {
		if v := g.Values(k); len(v) != 0 {
			t.Errorf("http upstream saw client %s = %q", k, v)
		}
	}
	if g.Get("X-Forwarded-For") != "1.2.3.4" || g.Get("X-Forwarded-Proto") != "http" || g.Get("X-Control") != "keep" {
		t.Errorf("authoritative/ordinary headers changed: XFF=%q proto=%q ctl=%q",
			g.Get("X-Forwarded-For"), g.Get("X-Forwarded-Proto"), g.Get("X-Control"))
	}

	raw := tunnelBackendCapture(t, clientForwardedSpoof)
	for k := range clientForwardedSpoof {
		if v := tunnelHeaderValues(raw, k); len(v) != 0 {
			t.Errorf("ws upstream saw client %s = %q", k, v)
		}
	}
	if v := tunnelHeaderValues(raw, "X-Forwarded-For"); len(v) != 1 || v[0] != "1.2.3.4" {
		t.Errorf("ws X-Forwarded-For = %q", v)
	}
}
