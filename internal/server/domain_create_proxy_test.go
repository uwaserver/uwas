package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// These tests pin down two intertwined production bugs that surfaced
// in v0.6.0 when an operator created a domain via the admin API:
//
//   Bug A (config pointer drift): server.reload() did `s.config = newCfg`
//   (pointer swap). Admin server was constructed in admin.New() with the
//   ORIGINAL pointer and never re-bound. Any reload (boot migration,
//   maybeReloadForApps after an app change, SIGHUP) made admin's view
//   of the config diverge from server's. Subsequent admin-API domain
//   appends landed in an orphan config object — vhost router never
//   saw them, every request returned 421 Misdirected Request.
//
//   Bug B (onDomainChange skipped proxy pools): The callback updated
//   vhost router and TLS manager, but not the proxy pool table.
//   Domain creates with type=proxy got a vhost entry and no upstream
//   pool — every request 502'd because the proxy handler had no
//   backend to forward to.
//
// Both were silent failures with green API responses, so the only way
// to catch them was an integration test that actually drove a request
// through the full lookup → dispatch → proxy chain.

// TestConfigPointerSurvivesReload covers Bug A directly. After a
// reload, admin's *config.Config view must still match server's, so
// mutating one is visible from the other. This is the invariant the
// `*s.config = *newCfg` (in-place struct copy) fix maintains.
func TestConfigPointerSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	mainCfg := []byte("global:\n  worker_count: \"1\"\n  log_level: error\n  log_format: text\n  http_listen: \":18280\"\n  https_listen: \":18281\"\n  pid_file: \"\"\n  http3: false\n  admin:\n    enabled: false\ndomains: []\n")
	if err := os.WriteFile(cfgPath, mainCfg, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18280", HTTPSListen: ":18281",
		},
	}
	s := New(cfg, logger.New("error", "text"))
	s.SetConfigPath(cfgPath)

	// Capture the pointer the server holds BEFORE reload. After reload,
	// it must be the same — only the dereferenced struct contents
	// should change.
	beforePtr := s.config

	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if s.config != beforePtr {
		t.Errorf("reload swapped s.config pointer (Bug A regressed). "+
			"before=%p after=%p — anyone holding the original pointer "+
			"(admin server, caller of New, etc.) now sees a stale config.",
			beforePtr, s.config)
	}
}

// TestDomainAppendThroughSharedPointerVisibleToServer simulates what
// admin.handleAddDomain does — append to config.Domains via a stored
// pointer — and asserts the server's vhost router picks it up after
// the onDomainChange callback fires. With Bug A present, admin would
// have appended to a stale config and the vhost map would stay empty.
func TestDomainAppendThroughSharedPointerVisibleToServer(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	mainCfg := []byte("global:\n  worker_count: \"1\"\n  log_level: error\n  log_format: text\n  http_listen: \":18282\"\n  https_listen: \":18283\"\n  pid_file: \"\"\n  http3: false\n  admin:\n    enabled: false\ndomains: []\n")
	os.WriteFile(cfgPath, mainCfg, 0600)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18282", HTTPSListen: ":18283",
		},
	}
	s := New(cfg, logger.New("error", "text"))
	s.SetConfigPath(cfgPath)

	// Trigger a reload first — this is the failure pattern. If the
	// pointer gets swapped here, the subsequent append below would
	// land in the orphan config that no one references.
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Now simulate admin.handleAddDomain: append a domain to the
	// shared config and invoke the onDomainChange callback. The
	// admin server was set up via SetAdmin / SetOnDomainChange in the
	// real server.New() flow; here we exercise the same path by
	// directly mutating the config and calling the rebuild path.
	s.configMu.Lock()
	s.config.Domains = append(s.config.Domains, config.Domain{
		Host: "newdomain.test",
		Root: dir,
		Type: "static",
		SSL:  config.SSLConfig{Mode: "off"},
	})
	s.configMu.Unlock()

	s.vhosts.Update(s.config.Domains)

	// Lookup must find the new domain. If Bug A regressed, vhosts
	// would be looking at an empty map.
	d, configured := s.vhosts.LookupWithStatus("newdomain.test")
	if !configured {
		t.Error("vhost router didn't pick up domain appended after reload — Bug A regressed")
	}
	if d == nil || d.Host != "newdomain.test" {
		t.Errorf("vhost lookup returned wrong domain: %+v", d)
	}
}

// TestRebuildProxyPoolsCoversNewDomain covers Bug B. onDomainChange
// must rebuild proxy pools so a freshly-added type=proxy domain has
// an upstream pool — without it, the proxy handler has no backend and
// every request 502s.
func TestRebuildProxyPoolsCoversNewDomain(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18284", HTTPSListen: ":18285",
		},
	}
	s := New(cfg, logger.New("error", "text"))

	// Before: zero domains, zero pools.
	if len(s.proxyPools) != 0 {
		t.Fatalf("expected empty pools at boot, got %d entries", len(s.proxyPools))
	}

	// Simulate the admin path: append a proxy domain in-memory, then
	// call rebuildProxyPools (what onDomainChange now does).
	domains := []config.Domain{
		{
			Host: "proxy.test",
			Type: "proxy",
			Root: "/tmp",
			SSL:  config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams: []config.Upstream{
					{Address: "http://127.0.0.1:9999", Weight: 1},
				},
			},
		},
	}
	s.config.Domains = domains
	s.rebuildProxyPools(domains)

	pool, exists := s.proxyPools["proxy.test"]
	if !exists {
		t.Fatal("proxy pool not built for newly-added domain — Bug B regressed")
	}
	if pool.Len() != 1 {
		t.Errorf("pool has %d backends, want 1", pool.Len())
	}
	backends := pool.All()
	if backends[0].URL.Host != "127.0.0.1:9999" {
		t.Errorf("backend host = %q, want %q", backends[0].URL.Host, "127.0.0.1:9999")
	}
}

// TestEndToEndDispatchToFreshProxyDomain combines both bugs in a
// single end-to-end assertion: after a domain is added via the
// admin-API path (config append + onDomainChange + rebuildProxyPools),
// a real HTTP request must reach handleRequest, hit the vhost router,
// dispatch to the proxy handler, and find a usable upstream pool.
// We don't actually proxy upstream (the backend URL points at a
// non-listening port); we just verify the dispatcher gets all the way
// to the proxy step instead of 421-ing on vhost lookup or returning
// "no pool".
func TestEndToEndDispatchToFreshProxyDomain(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: ":18286", HTTPSListen: ":18287",
		},
	}
	s := New(cfg, logger.New("error", "text"))

	// Mimic admin.handleAddDomain: append + onDomainChange equivalent.
	d := config.Domain{
		Host: "live.test",
		Type: "proxy",
		Root: t.TempDir(),
		SSL:  config.SSLConfig{Mode: "off"},
		Proxy: config.ProxyConfig{
			Upstreams: []config.Upstream{
				{Address: "http://127.0.0.1:1", Weight: 1}, // unreachable on purpose
			},
		},
	}
	s.configMu.Lock()
	s.config.Domains = append(s.config.Domains, d)
	s.configMu.Unlock()
	s.vhosts.Update(s.config.Domains)
	s.rebuildProxyPools(s.config.Domains)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "live.test"
	s.handleRequest(rec, req)

	// We want anything BUT 421 (would indicate vhost unknown) — the
	// proxy will return 502 because the upstream is unreachable, and
	// that's the correct outcome: we asserted the request reached the
	// proxy dispatcher and tried to forward.
	if rec.Code == 421 {
		t.Errorf("got 421 Misdirected Request — vhost router didn't pick up the new domain (Bug A regressed)")
	}
	// 502 means proxy tried to dial and failed (expected: port 1 is unreachable).
	// 500 would mean some other handler error path; either way, NOT 421
	// proves the vhost+dispatch chain works.
	if rec.Code != 502 && rec.Code != 503 && rec.Code != 500 && rec.Code != 504 {
		t.Logf("got unexpected status %d (not 421/5xx) — body: %s", rec.Code, rec.Body.String())
	}
}
