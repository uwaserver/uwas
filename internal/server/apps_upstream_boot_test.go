package server

import (
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// bootServerWithApp builds a server whose only domain proxies to
// `apps://demo`, plus a registered-but-not-yet-started app named demo.
// This is the boot state: pools already built, apps still down.
func bootServerWithApp(t *testing.T) (*Server, *apps.Manager) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18390", HTTPSListen: ":18391",
		},
		Domains: []config.Domain{{
			Host: "app.test",
			Type: "proxy",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams: []config.Upstream{{Address: "apps://demo", Weight: 1}},
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))

	mgr := apps.NewManager(apps.NewStore(filepath.Join(dir, "apps.d")), nil)
	s.appsMgr = mgr
	if err := mgr.Register(&apps.App{
		Name:    "demo",
		Runtime: apps.RuntimeCustom,
		Command: "sleep 30",
		WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("register demo app: %v", err)
	}
	t.Cleanup(mgr.StopAll)
	return s, mgr
}

func poolHost(t *testing.T, s *Server, host string) string {
	t.Helper()
	pool, ok := s.proxyPools[host]
	if !ok {
		t.Fatalf("no proxy pool for %s", host)
	}
	backends := pool.All()
	if len(backends) != 1 {
		t.Fatalf("pool has %d backends, want 1", len(backends))
	}
	return backends[0].URL.Host
}

// The bug: starting the apps without rebuilding the pools leaves every
// apps:// upstream pinned to the 127.0.0.1:0 placeholder that New()
// resolved while the app was still down. Requests then fail with
// ECONNREFUSED — "502 Bad Gateway, upstream refused connection" — even
// though the app is running perfectly well.
func TestAppsUpstreamStaysPlaceholderWhenPoolsNotRebuilt(t *testing.T) {
	s, mgr := bootServerWithApp(t)

	if got := poolHost(t, s, "app.test"); got != "127.0.0.1:0" {
		t.Fatalf("pre-start pool host = %q, want the 127.0.0.1:0 placeholder", got)
	}

	mgr.StartAll() // the old boot path: start apps, never re-resolve

	// Guard against a vacuous pass: if the app never came up, the stale
	// placeholder below would be correct rather than a bug.
	live := mgr.ListenAddrForPort("demo", 0)
	if live == "" {
		t.Fatal("demo app did not start — test cannot distinguish stale from correct")
	}

	if got := poolHost(t, s, "app.test"); got != "127.0.0.1:0" {
		t.Fatalf("pool host = %q; expected it to still hold the stale placeholder", got)
	}
	t.Logf("app is live at %s but the pool still points at 127.0.0.1:0", live)
}

// The fix: startRegisteredApps re-resolves the pools after StartAll, so
// the upstream tracks the port the supervisor actually assigned.
func TestStartRegisteredAppsReResolvesUpstreams(t *testing.T) {
	s, mgr := bootServerWithApp(t)

	s.startRegisteredApps()

	live := mgr.ListenAddrForPort("demo", 0)
	if live == "" {
		t.Fatal("demo app did not start")
	}
	if got := poolHost(t, s, "app.test"); got != live {
		t.Fatalf("pool host = %q, want the live app address %q", got, live)
	}
}

// A nil supervisor must not panic or disturb existing pools.
func TestStartRegisteredAppsNilManager(t *testing.T) {
	s, _ := bootServerWithApp(t)
	s.appsMgr = nil
	s.startRegisteredApps()
	if got := poolHost(t, s, "app.test"); got != "127.0.0.1:0" {
		t.Fatalf("pool host = %q, want unchanged placeholder", got)
	}
}
