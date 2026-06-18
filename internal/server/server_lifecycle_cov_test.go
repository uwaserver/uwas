package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestNewFullFeatured constructs a Server with admin + multi-user auth + MCP +
// backup + a fully-featured proxy domain (health check / circuit breaker /
// canary / mirror), exercising the feature-gated branches of New() and the
// proxy-pool build loop. It then drives SetConfigPath through its admin +
// backup wiring.
func TestNewFullFeatured(t *testing.T) {
	webroot := t.TempDir()
	domainRoot := t.TempDir()
	os.WriteFile(filepath.Join(domainRoot, "index.html"), []byte("x"), 0644)
	backupDir := t.TempDir()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}\n"), 0644)

	cfg := &config.Config{
		DomainsDir: "domains.d", // relative → SetConfigPath resolves against cfg dir
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			WebRoot:     webroot,
			Admin: config.AdminConfig{
				Enabled: true,
				Listen:  "127.0.0.1:0",
				APIKey:  "abcdefghijklmnopqrstuvwxyz",
			},
			Users: config.UsersConfig{
				Enabled:                    true,
				AllowResller:               true,
				AllowLegacyPlaintextAPIKey: true,
			},
			MCP: config.MCPConfig{Enabled: true, Listen: "127.0.0.1:0"},
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(4 << 20),
				DiskPath:    t.TempDir(),
				DiskLimit:   config.ByteSize(4 << 20),
			},
			Backup: config.BackupConfig{
				Enabled:  true,
				Provider: "local",
				Local:    config.BackupLocalConfig{Path: backupDir},
			},
		},
		Domains: []config.Domain{
			{
				Host: "full2.test",
				Type: "proxy",
				Root: domainRoot,
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{{Address: "http://127.0.0.1:65501", Weight: 1}},
					Algorithm: "round_robin",
					HealthCheck: config.HealthCheckConfig{
						Path:     "/h",
						Interval: config.Duration{Duration: time.Hour},
						Timeout:  config.Duration{Duration: time.Second},
					},
					CircuitBreaker: config.CircuitConfig{Threshold: 3, Timeout: config.Duration{Duration: time.Minute}},
					Canary: config.CanaryConfig{
						Enabled:   true,
						Upstreams: []config.Upstream{{Address: "http://127.0.0.1:65502"}},
					},
					Mirror: config.MirrorConfig{Enabled: true, Backend: "http://127.0.0.1:65503"},
				},
			},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })

	if s.admin == nil {
		t.Fatal("admin not initialized")
	}
	if s.authMgr == nil {
		t.Fatal("auth manager not initialized for multi-user")
	}
	if s.mcp == nil {
		t.Fatal("MCP not initialized")
	}
	if s.backupMgr == nil {
		t.Fatal("backup manager not initialized")
	}
	pool, _, cb, mir, can := s.proxyRouteFor("full2.test")
	if pool == nil || cb == nil || mir == nil || can == nil {
		t.Fatalf("proxy features not built in New: pool=%v cb=%v mir=%v can=%v",
			pool != nil, cb != nil, mir != nil, can != nil)
	}

	// SetConfigPath wires admin + backup paths (incl. domain content roots).
	s.SetConfigPath(cfgPath)
	if s.configPath != cfgPath {
		t.Errorf("configPath not set")
	}

	// Trigger the wired bandwidth alert closure (Record → alertFn → alerter).
	s.bwMgr.UpdateDomains([]config.Domain{{
		Host: "full2.test",
		Bandwidth: config.BandwidthConfig{
			Enabled:    true,
			DailyLimit: config.ByteSize(100),
			Action:     "alert",
		},
	}})
	// 90% threshold then 100% to exercise both alert arms.
	s.bwMgr.Record("full2.test", 90)
	s.bwMgr.Record("full2.test", 20)
}

// TestStartWithSFTPAndHTTP3 runs Start() with SFTP + HTTP3 + backup-cron
// enabled so the SFTP user-provisioning loop, HTTP/3 listener attempt, and
// backup cron scheduler branches in Start() execute. The server is cancelled
// shortly after boot.
func TestStartWithSFTPAndHTTP3(t *testing.T) {
	domainRoot := t.TempDir()
	os.WriteFile(filepath.Join(domainRoot, "index.html"), []byte("x"), 0644)
	backupDir := t.TempDir()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount:  "1",
			LogLevel:     "error",
			LogFormat:    "text",
			HTTPListen:   "127.0.0.1:0",
			HTTPSListen:  "127.0.0.1:0",
			SFTPListen:   "127.0.0.1:0",
			HTTP3Enabled: true,
			Admin:        config.AdminConfig{APIKey: "abcdefghijklmnopqrstuvwxyz"},
			Backup: config.BackupConfig{
				Enabled:  true,
				Provider: "local",
				Cron:     "0 2 * * *",
				Local:    config.BackupLocalConfig{Path: backupDir},
			},
		},
		Domains: []config.Domain{
			{Host: "sftp.test", Type: "static", Root: domainRoot, SSL: config.SSLConfig{Mode: "manual"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))

	go func() {
		time.Sleep(250 * time.Millisecond)
		s.cancel()
	}()
	if err := s.Start(); err != nil {
		t.Errorf("Start returned error: %v", err)
	}
}

// TestStartInvalidWorkerCount covers the worker_count fallback branch.
func TestStartInvalidWorkerCount(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "not-a-number",
			LogLevel:    "error",
			LogFormat:   "text",
			HTTPListen:  "127.0.0.1:0",
		},
		Domains: []config.Domain{
			{Host: "wc.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.cancel()
	}()
	if err := s.Start(); err != nil {
		t.Errorf("Start returned error: %v", err)
	}
}

// TestStartHTTPListenError covers the startHTTP error-return branch by binding
// to an already-used / invalid listen address.
func TestStartHTTPListenError(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			HTTPListen:  "256.256.256.256:99999", // invalid → listen fails
		},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	if err := s.Start(); err == nil {
		t.Errorf("expected Start error for invalid HTTP listen address")
	}
}
