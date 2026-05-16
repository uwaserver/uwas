package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// These tests pin down three boot-migration bugs that escaped unit-level
// coverage and were caught only by an end-to-end smoke test in v0.6.0.
// Each one would silently regress to a "looks fine in logs, breaks in
// production" failure mode without an integration-level assertion.

// migrationFixture sets up a Server with one legacy `type=app` domain
// on disk and in memory, plus a fresh apps.Manager and configPath
// pointing at the on-disk YAML. Mirrors the boot sequence in
// server.Start(): SetConfigPath, SetAppsManager, then caller invokes
// MigrateLegacyAppsAtBoot.
type migrationFixture struct {
	server     *Server
	configDir  string
	appsDir    string
	domainHost string
	appName    string
}

func newMigrationFixture(t *testing.T) *migrationFixture {
	t.Helper()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	domainsDir := filepath.Join(configDir, "domains.d")
	appsDir := filepath.Join(dir, "apps.d")
	for _, p := range []string{configDir, domainsDir, appsDir} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}

	// Write a legacy type=app domain YAML to disk so the migrator has
	// something to rewrite. The in-memory config below has the matching
	// entry — server boot loads both in lockstep.
	domainYAML := []byte(`host: legacyapp.test
root: /tmp/legacy-root
type: app
ssl:
  mode: "off"
app:
  runtime: node
  command: node server.js
  port: 18999
`)
	domainPath := filepath.Join(domainsDir, "legacyapp.test.yaml")
	if err := os.WriteFile(domainPath, domainYAML, 0600); err != nil {
		t.Fatalf("write domain yaml: %v", err)
	}

	cfg := &config.Config{
		Global:     config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}, WebRoot: dir},
		DomainsDir: domainsDir,
		Domains: []config.Domain{
			{
				Host: "legacyapp.test",
				Root: "/tmp/legacy-root",
				Type: "app",
				SSL:  config.SSLConfig{Mode: "off"},
				App: config.AppConfig{
					Runtime: "node",
					Command: "node server.js",
					Port:    18999,
				},
			},
		},
	}

	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)

	// Critical ordering: SetConfigPath before MigrateLegacyAppsAtBoot.
	// Bug #1 was that the migration ran inside server.New(), BEFORE the
	// CLI got a chance to call SetConfigPath. The on-disk rewrite then
	// silently no-op'd. We replicate the correct order here.
	mainCfgPath := filepath.Join(configDir, "uwas.yaml")
	s.SetConfigPath(mainCfgPath)

	store := apps.NewStore(appsDir)
	mgr := apps.NewManager(store, log)
	s.SetAppsManager(mgr)

	return &migrationFixture{
		server:     s,
		configDir:  configDir,
		appsDir:    appsDir,
		domainHost: "legacyapp.test",
		appName:    "legacyapp-test",
	}
}

// readDomainYAML reads the on-disk YAML for the legacy domain after
// the migration has run, so assertions can confirm the rewrite landed
// (or didn't, depending on the bug being checked).
func (f *migrationFixture) readDomainYAML(t *testing.T) config.Domain {
	t.Helper()
	path := filepath.Join(f.configDir, "domains.d", f.domainHost+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read domain yaml: %v", err)
	}
	var d config.Domain
	if err := yaml.Unmarshal(data, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return d
}

// TestBootMigration_RewritesDomainYAMLOnDisk is the regression test
// for bug #1: MigrateLegacyAppsAtBoot used to run inside server.New()
// before SetConfigPath had set s.configPath, so domainFilePath()
// returned "config path not set" and the on-disk rewrite was skipped.
// Every subsequent boot would re-migrate the same domain, and any
// operator-initiated config reload would revert the in-memory change.
//
// The assertion: after migration, the YAML file on disk MUST reflect
// type=proxy with an apps:// upstream — not the original type=app.
func TestBootMigration_RewritesDomainYAMLOnDisk(t *testing.T) {
	f := newMigrationFixture(t)

	migrated := f.server.MigrateLegacyAppsAtBoot()
	if migrated != 1 {
		t.Fatalf("MigrateLegacyAppsAtBoot returned %d, want 1", migrated)
	}

	d := f.readDomainYAML(t)
	if d.Type != "proxy" {
		t.Errorf("on-disk type = %q, want %q (migration didn't rewrite YAML — bug #1 regressed)",
			d.Type, "proxy")
	}
	if len(d.Proxy.Upstreams) != 1 {
		t.Fatalf("on-disk proxy upstreams = %d, want 1", len(d.Proxy.Upstreams))
	}
	want := "apps://" + f.appName
	if d.Proxy.Upstreams[0].Address != want {
		t.Errorf("on-disk upstream = %q, want %q", d.Proxy.Upstreams[0].Address, want)
	}
	// The legacy App block should be cleared — leaving it set would
	// confuse a future config reader into thinking both forms apply.
	if d.App.Runtime != "" || d.App.Command != "" {
		t.Errorf("on-disk App block should be cleared after migration, got runtime=%q command=%q",
			d.App.Runtime, d.App.Command)
	}
}

// TestBootMigration_DoesNotStartApps is the regression test for bug
// #2: MigrateLegacyAppsAtBoot used to call s.appsMgr.Start() on every
// migrated app. The caller then ran s.appsMgr.StartAll(), which tried
// to start the SAME processes again and emitted "already running"
// errors on every boot.
//
// The assertion: after migration, the app is REGISTERED but NOT
// running. The caller (server.Start) is the single source of truth
// for starting things; the migrator only converts.
func TestBootMigration_DoesNotStartApps(t *testing.T) {
	f := newMigrationFixture(t)

	migrated := f.server.MigrateLegacyAppsAtBoot()
	if migrated != 1 {
		t.Fatalf("MigrateLegacyAppsAtBoot returned %d, want 1", migrated)
	}

	// App must exist in the supervisor's registry.
	def, err := f.server.appsMgr.Store().Get(f.appName)
	if err != nil {
		t.Fatalf("Store().Get(%q): %v", f.appName, err)
	}
	if def == nil {
		t.Fatalf("app %q not registered after migration", f.appName)
	}

	// And it must NOT be running. ListenAddr returns "" for a
	// registered-but-stopped app — that's the proof.
	if addr := f.server.appsMgr.ListenAddr(f.appName); addr != "" {
		t.Errorf("ListenAddr(%q) = %q, want empty (migration must not auto-start — bug #2 regressed)",
			f.appName, addr)
	}
}

// TestBootMigration_InMemoryConfigUpdated checks that the in-memory
// s.config.Domains entry was rewritten alongside the on-disk YAML.
// Without this, the router would still see type=app and dispatch to
// the legacy handler (which is now just a 502 stub) while the
// supervisor has the app running and ready to serve.
func TestBootMigration_InMemoryConfigUpdated(t *testing.T) {
	f := newMigrationFixture(t)

	if migrated := f.server.MigrateLegacyAppsAtBoot(); migrated != 1 {
		t.Fatalf("MigrateLegacyAppsAtBoot returned %d, want 1", migrated)
	}

	f.server.configMu.RLock()
	defer f.server.configMu.RUnlock()

	var found *config.Domain
	for i := range f.server.config.Domains {
		if f.server.config.Domains[i].Host == f.domainHost {
			found = &f.server.config.Domains[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("domain %q missing from in-memory config", f.domainHost)
	}
	if found.Type != "proxy" {
		t.Errorf("in-memory domain.Type = %q, want %q", found.Type, "proxy")
	}
	if len(found.Proxy.Upstreams) != 1 {
		t.Fatalf("in-memory proxy upstreams = %d, want 1", len(found.Proxy.Upstreams))
	}
	if !strings.HasPrefix(found.Proxy.Upstreams[0].Address, "apps://") {
		t.Errorf("in-memory upstream = %q, want apps:// prefix",
			found.Proxy.Upstreams[0].Address)
	}
}

// TestBootMigration_Idempotent verifies the second boot is a no-op.
// The on-disk YAML now has type=proxy, the in-memory config matches,
// and there are no legacy entries to convert. Running migration again
// must return 0 and not duplicate the apps.d/ entry.
func TestBootMigration_Idempotent(t *testing.T) {
	f := newMigrationFixture(t)

	// First run — does the work.
	if migrated := f.server.MigrateLegacyAppsAtBoot(); migrated != 1 {
		t.Fatalf("first run migrated = %d, want 1", migrated)
	}

	// Reload the in-memory config from the rewritten on-disk YAML so
	// the second run sees what a fresh boot would see.
	d := f.readDomainYAML(t)
	f.server.configMu.Lock()
	for i := range f.server.config.Domains {
		if f.server.config.Domains[i].Host == f.domainHost {
			f.server.config.Domains[i] = d
		}
	}
	f.server.configMu.Unlock()

	// Second run — must be a clean no-op.
	if migrated := f.server.MigrateLegacyAppsAtBoot(); migrated != 0 {
		t.Errorf("second run migrated = %d, want 0 (migration not idempotent)", migrated)
	}
}

// TestBootMigration_NoLegacyDomainsIsNoOp covers the common case
// once the deployment has been on v0.6+ for a while — there are no
// type=app domains left. The fast-path early-return saves us from
// touching disk on every boot.
func TestBootMigration_NoLegacyDomainsIsNoOp(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	os.MkdirAll(filepath.Join(configDir, "domains.d"), 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}, WebRoot: dir},
		Domains: []config.Domain{
			{Host: "static.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"}},
			{Host: "proxy.test", Type: "proxy", SSL: config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:9000"}}}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log, metrics.New())
	s.SetConfigPath(filepath.Join(configDir, "uwas.yaml"))

	store := apps.NewStore(filepath.Join(dir, "apps.d"))
	s.SetAppsManager(apps.NewManager(store, log))

	if migrated := s.MigrateLegacyAppsAtBoot(); migrated != 0 {
		t.Errorf("MigrateLegacyAppsAtBoot with no legacy domains returned %d, want 0", migrated)
	}
}

// TestBootMigration_NoAppsManagerIsNoOp guards against a nil-pointer
// crash when the apps manager hasn't been wired yet — happens during
// test teardown and in degraded boot paths.
func TestBootMigration_NoAppsManagerIsNoOp(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}},
		Domains: []config.Domain{
			{Host: "legacy.test", Type: "app", Root: "/tmp/x", SSL: config.SSLConfig{Mode: "off"},
				App: config.AppConfig{Runtime: "node", Command: "x", Port: 1234}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log, metrics.New())
	// Deliberately no SetAppsManager.

	if migrated := s.MigrateLegacyAppsAtBoot(); migrated != 0 {
		t.Errorf("MigrateLegacyAppsAtBoot with nil appsMgr returned %d, want 0", migrated)
	}
}
