package admin

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
)

// appBackedHealthFixture stands up a real HTTP server on loopback, then
// registers an app the supervisor reports as listening on that exact
// port, and points a proxy domain at it via an apps:// upstream.
func appBackedHealthFixture(t *testing.T, code int) (*Server, string) {
	t.Helper()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
	t.Cleanup(backend.Close)

	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	if err != nil {
		t.Fatalf("split backend addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse backend port: %v", err)
	}

	mgr := apps.NewManager(apps.NewStore(filepath.Join(t.TempDir(), "apps.d")), nil)
	// RuntimeCustom skips the start-time port availability check, so the
	// supervisor keeps the port our backend is already listening on.
	if err := mgr.Register(&apps.App{
		Name:    "demo",
		Runtime: apps.RuntimeCustom,
		Command: "sleep 30",
		Port:    port,
		WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("register app: %v", err)
	}
	if err := mgr.Start("demo"); err != nil {
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(mgr.StopAll)

	if got := mgr.ListenAddr("demo"); got != "127.0.0.1:"+portStr {
		t.Fatalf("supervisor reports %q, want 127.0.0.1:%s", got, portStr)
	}

	s := testServer()
	s.SetAppsManager(mgr)
	s.config.Domains = []config.Domain{{
		Host: "app.example.com",
		Type: "proxy",
		SSL:  config.SSLConfig{Mode: "off"},
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "apps://demo", Weight: 1}},
		},
	}}
	return s, "127.0.0.1:" + portStr
}

// healthItemFor picks the record for one hostname. A domain yields a
// record per hostname (the host plus its www. alias), so tests must
// select rather than assume a single item.
func healthItemFor(t *testing.T, s *Server, host string) struct {
	Host   string `json:"host"`
	Target string `json:"target"`
	Status string `json:"status"`
	Code   int    `json:"code"`
	Error  string `json:"error"`
} {
	t.Helper()
	for _, it := range healthItems(t, s) {
		if it.Host == host {
			return it
		}
	}
	t.Fatalf("no health record for %q", host)
	panic("unreachable")
}

func healthItems(t *testing.T, s *Server) []struct {
	Host   string `json:"host"`
	Target string `json:"target"`
	Status string `json:"status"`
	Code   int    `json:"code"`
	Error  string `json:"error"`
} {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []struct {
			Host   string `json:"host"`
			Target string `json:"target"`
			Status string `json:"status"`
			Code   int    `json:"code"`
			Error  string `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	return body.Items
}

// A domain backed by a healthy apps:// upstream must report "up". The
// probe target is the loopback address the supervisor assigned, which
// the SSRF guard is built to refuse — applying that guard here marked
// every app-backed domain "down" no matter how healthy the app was, and
// the dashboard rendered the result as "Inactive".
func TestDomainHealthAppUpstreamReportsUp(t *testing.T) {
	s, addr := appBackedHealthFixture(t, http.StatusOK)

	got := healthItemFor(t, s, "app.example.com")

	if got.Target != "http://"+addr+"/" {
		t.Fatalf("probe target = %q, want the supervisor's loopback address http://%s/", got.Target, addr)
	}
	if strings.Contains(got.Error, "blocked") {
		t.Fatalf("probe was blocked by the SSRF guard: %q", got.Error)
	}
	if got.Status != "up" || got.Code != http.StatusOK {
		t.Fatalf("status = %q code = %d, want up/200 (error: %q)", got.Status, got.Code, got.Error)
	}
}

// A failing app must still report a failure — the fix must not turn the
// check into something that always says "up".
func TestDomainHealthAppUpstreamReportsErrorOn5xx(t *testing.T) {
	s, _ := appBackedHealthFixture(t, http.StatusBadGateway)

	got := healthItemFor(t, s, "app.example.com")
	if got.Status != "error" || got.Code != http.StatusBadGateway {
		t.Fatalf("status = %q code = %d, want error/502", got.Status, got.Code)
	}
}

// Domains that are not app-backed keep the SSRF guard: a loopback Host
// must still be refused, so the fix cannot be used to probe internal
// addresses by pointing a domain at them.
func TestDomainHealthNonAppLoopbackStillBlocked(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	s := testServer()
	s.config.Domains = []config.Domain{{
		Host: strings.TrimPrefix(backend.URL, "http://"),
		Type: "static",
		SSL:  config.SSLConfig{Mode: "off"},
	}}

	got := healthItemFor(t, s, strings.TrimPrefix(backend.URL, "http://"))
	if got.Status != "down" || !strings.Contains(got.Error, "blocked") {
		t.Fatalf("status = %q error = %q, want down + blocked", got.Status, got.Error)
	}
}

// The apps:// path reports "down" with a clear reason when the app is
// registered but not running.
func TestDomainHealthAppUpstreamNotListening(t *testing.T) {
	mgr := apps.NewManager(apps.NewStore(filepath.Join(t.TempDir(), "apps.d")), nil)
	if err := mgr.Register(&apps.App{
		Name:    "stopped",
		Runtime: apps.RuntimeCustom,
		Command: "./run",
	}); err != nil {
		t.Fatalf("register app: %v", err)
	}

	s := testServer()
	s.SetAppsManager(mgr)
	s.config.Domains = []config.Domain{{
		Host: "stopped.example.com",
		Type: "proxy",
		SSL:  config.SSLConfig{Mode: "off"},
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "apps://stopped", Weight: 1}},
		},
	}}

	got := healthItemFor(t, s, "stopped.example.com")
	if got.Status != "down" || !strings.Contains(got.Error, "not listening") {
		t.Fatalf("status = %q error = %q, want down + not listening", got.Status, got.Error)
	}
}
