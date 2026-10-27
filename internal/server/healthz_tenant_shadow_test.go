package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F28201: "/healthz" was answered by UWAS itself for every Host, so a proxied
// application's own /healthz (the usual liveness endpoint) was unreachable and
// a probe of a site whose upstream was down read "ok". The namespaced
// /.well-known/health stays server-wide; the bare alias belongs to the server
// off a proxy site's own Host (IP, localhost, static and PHP sites keep it).
func TestHealthzDoesNotShadowTenantApplication(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("app-down"))
	}))
	defer up.Close()

	s := newDispatchTestServer(t, []config.Domain{{
		Host: "app.test", Type: "proxy", SSL: config.SSLConfig{Mode: "off"},
		Proxy: config.ProxyConfig{
			Upstreams:             []config.Upstream{{Address: up.URL}},
			AllowPrivateUpstreams: true,
		},
	}})
	s.rebuildProxyPools(s.config.Domains)

	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		s.handleRequest(rec, req)
		return rec
	}

	rec := get("app.test", "/healthz")
	t.Logf("EXPECTED: tenant upstream answers (503 app-down)\nACTUAL: %d %q", rec.Code, rec.Body.String())
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "app-down" {
		t.Fatalf("PROBLEM CONFIRMED: UWAS answered the tenant's /healthz itself: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("app.test", "/.well-known/health"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("namespaced health endpoint = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("203.0.113.9:80", "/healthz"); rec.Code != 200 {
		t.Fatalf("/healthz on an unconfigured Host = %d, want 200", rec.Code)
	}
}
