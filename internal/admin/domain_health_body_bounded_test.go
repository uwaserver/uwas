package admin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
)

// The domain health check only needs the status line, but it used to read the
// tenant's whole page. A page that answers "/" with a very large (or endless)
// body made every check pull as much as the 5 s timeout allowed, from every
// domain at once. The probe now reads a bounded prefix and closes.
func TestDomainHealthProbeReadsBoundedBody(t *testing.T) {
	const total = 256 << 20
	var sent atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("x", 64<<10))
		fl, _ := w.(http.Flusher)
		for sent.Load() < total {
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(backend.Close)

	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	mgr := apps.NewManager(apps.NewStore(filepath.Join(t.TempDir(), "apps.d")), nil)
	if err := mgr.Register(&apps.App{
		Name: "demo", Runtime: apps.RuntimeCustom, Command: "sleep 30", Port: port, WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start("demo"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.StopAll)

	s := testServer()
	s.SetAppsManager(mgr)
	s.config.Domains = []config.Domain{{
		Host: "big.example.com",
		Type: "proxy",
		SSL:  config.SSLConfig{Mode: "off"},
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{{Address: "apps://demo", Weight: 1}},
		},
	}}

	it := healthItemFor(t, s, "big.example.com")
	got := sent.Load()
	t.Logf("EXPECTED: well under %d bytes pulled from the page\nACTUAL: %d bytes written by the page", total, got)
	if it.Status != "up" || it.Code != http.StatusOK {
		t.Errorf("status = %q code = %d, want up/200 (the status line is all the check needs)", it.Status, it.Code)
	}
	// Bound: the read limit plus what the kernel and net/http buffer in flight.
	if got > 32<<20 {
		t.Fatalf("PROBLEM CONFIRMED: the health probe pulled %d bytes of a page body", got)
	}
}
