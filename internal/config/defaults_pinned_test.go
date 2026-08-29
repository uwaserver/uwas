package config

import (
	"testing"
	"time"
)

// TestAppliedDefaultsArePinned records the defaults the dashboard shows as
// placeholders in an empty settings field.
//
// They drifted apart once already: the panel offered 300 for default_ttl
// against a real 3600, 60 for grace_ttl against 86400, 256MB against 512MB,
// and `:9443` for the admin listener against a loopback-only default — that
// last one reading as an invitation to bind every interface. An operator
// reads a placeholder as "this is what you get if you leave it blank".
//
// Changing a value here means changing the matching placeholder in
// web/dashboard/src/pages/settingsSections.tsx.
func TestAppliedDefaultsArePinned(t *testing.T) {
	var cfg Config
	applyDefaults(&cfg)
	g := cfg.Global

	strs := []struct {
		name string
		got  string
		want string
	}{
		{"worker_count", g.WorkerCount, "auto"},
		{"http_listen", g.HTTPListen, ":80"},
		{"https_listen", g.HTTPSListen, ":443"},
		{"pid_file", g.PIDFile, "/var/run/uwas.pid"},
		{"web_root", g.WebRoot, "/var/www"},
		{"log_level", g.LogLevel, "info"},
		{"log_format", g.LogFormat, "text"},
		{"admin.listen", g.Admin.Listen, "127.0.0.1:9443"},
		{"mcp.listen", g.MCP.Listen, "127.0.0.1:9444"},
		{"acme.storage", g.ACME.Storage, "/var/lib/uwas/certs"},
		{"acme.ca_url", g.ACME.CAURL, "https://acme-v02.api.letsencrypt.org/directory"},
		{"cache.disk_path", g.Cache.DiskPath, "/var/cache/uwas"},
	}
	for _, tt := range strs {
		if tt.got != tt.want {
			t.Errorf("%s default = %q, want %q (update the dashboard placeholder too)", tt.name, tt.got, tt.want)
		}
	}

	ints := []struct {
		name string
		got  int64
		want int64
	}{
		{"max_connections", int64(g.MaxConnections), 65536},
		{"timeouts.max_header_bytes", int64(g.Timeouts.MaxHeaderBytes), 1 << 20},
		{"cache.memory_limit", int64(g.Cache.MemoryLimit), 512 * 1024 * 1024},
		{"cache.disk_limit", int64(g.Cache.DiskLimit), 10 * 1024 * 1024 * 1024},
		{"cache.default_ttl", int64(g.Cache.DefaultTTL), 3600},
		{"cache.grace_ttl", int64(g.Cache.GraceTTL), 86400},
		{"static_cache.max_file_size", int64(g.StaticCache.MaxFileSize), 512 * 1024},
		{"static_cache.max_bytes", int64(g.StaticCache.MaxBytes), 64 * 1024 * 1024},
	}
	for _, tt := range ints {
		if tt.got != tt.want {
			t.Errorf("%s default = %d, want %d (update the dashboard placeholder too)", tt.name, tt.got, tt.want)
		}
	}

	durs := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"timeouts.read", g.Timeouts.Read.Duration, 30 * time.Second},
		{"timeouts.read_header", g.Timeouts.ReadHeader.Duration, 10 * time.Second},
		{"timeouts.write", g.Timeouts.Write.Duration, 60 * time.Second},
		{"timeouts.idle", g.Timeouts.Idle.Duration, 120 * time.Second},
		{"timeouts.shutdown_grace", g.Timeouts.ShutdownGrace.Duration, 30 * time.Second},
		{"static_cache.revalidate", g.StaticCache.Revalidate.Duration, time.Second},
	}
	for _, tt := range durs {
		if tt.got != tt.want {
			t.Errorf("%s default = %v, want %v (update the dashboard placeholder too)", tt.name, tt.got, tt.want)
		}
	}
}

// TestHtaccessModeIsValidated covers the enum that used to accept anything.
// Only "import" turns the engine on, so `mode: on` — the obvious thing to
// write — meant "off" with nothing to say so.
func TestHtaccessModeIsValidated(t *testing.T) {
	for _, mode := range []string{"on", "enabled", "true", "yes", "Import"} {
		d := Domain{Host: "a.test", Type: "static", Root: "/tmp", Htaccess: HtaccessConfig{Mode: mode}}
		if err := validateDomain(&d, false); err == nil {
			t.Errorf("htaccess.mode = %q was accepted; it silently means off", mode)
		}
	}
	for _, mode := range []string{"", "off", "import"} {
		d := Domain{Host: "a.test", Type: "static", Root: "/tmp", Htaccess: HtaccessConfig{Mode: mode}}
		if err := validateDomain(&d, false); err != nil {
			t.Errorf("htaccess.mode = %q rejected: %v", mode, err)
		}
	}
}
