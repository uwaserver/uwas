package admin

import (
	"testing"
)

// The settings API is an allow-list in both directions: a key absent from the
// GET map never reaches the panel, and a key absent from the PUT switch is
// accepted with 200 and silently dropped. The autoblock/watchdog dashboard
// sections are worth nothing unless both ends carry every key, so this tests
// the round trip rather than the struct — the same failure mode that left
// rate_limit.by and waf.rules dead for releases.

func TestAutoBlockSettingsRoundTrip(t *testing.T) {
	s := settingsTestServer(t)

	// Every key the dashboard's autoblock + watchdog sections read must appear
	// in GET, or the panel renders blanks.
	want := []string{
		"global.autoblock.enabled", "global.autoblock.dry_run",
		"global.autoblock.firewall_sync", "global.autoblock.feed_rate_hits", "global.autoblock.window",
		"global.autoblock.max_connections", "global.autoblock.max_aborts",
		"global.autoblock.max_concurrent", "global.autoblock.max_waf_hits",
		"global.autoblock.max_rate_hits", "global.autoblock.max_not_found",
		"global.autoblock.block_duration", "global.autoblock.max_block_duration",
		"global.autoblock.escalate", "global.autoblock.state_path",
		"global.autoblock.whitelist",
		"global.rate_limit.requests", "global.rate_limit.window",
		"global.users.session_ttl", "global.trusted_proxies",
		"global.watchdog.enabled", "global.watchdog.interval",
		"global.watchdog.timeout", "global.watchdog.failures",
		"global.watchdog.self_restart",
	}
	body := settingsBody(t, s)
	for _, k := range want {
		if _, ok := body[k]; !ok {
			t.Errorf("settings GET missing %q — the panel would render a blank field", k)
		}
	}

	// And every one has to survive a PUT, or the toggle saves nothing.
	putSettings(t, s, `{
		"global.autoblock.enabled": "true",
		"global.autoblock.dry_run": "false",
		"global.autoblock.firewall_sync": "true",
		"global.autoblock.feed_rate_hits": "false",
		"global.autoblock.window": "30s",
		"global.autoblock.max_aborts": 42,
		"global.autoblock.max_connections": 999,
		"global.autoblock.escalate": "true",
		"global.autoblock.block_duration": "20m",
		"global.autoblock.state_path": "/tmp/ab.json",
		"global.autoblock.whitelist": "203.0.113.0/24\n198.51.100.10",
		"global.rate_limit.requests": 600,
		"global.rate_limit.window": "60s",
		"global.users.session_ttl": 12,
		"global.trusted_proxies": "10.0.0.0/8\n172.16.0.0/12",
		"global.watchdog.enabled": "true",
		"global.watchdog.interval": "10s",
		"global.watchdog.failures": 5,
		"global.watchdog.self_restart": "true"
	}`)

	got := settingsBody(t, s)
	checks := map[string]any{
		"global.autoblock.enabled":         true,
		"global.autoblock.dry_run":         false,
		"global.autoblock.firewall_sync":   true,
		"global.autoblock.window":          "30s",
		"global.autoblock.max_aborts":      float64(42),
		"global.autoblock.max_connections": float64(999),
		"global.autoblock.escalate":        true,
		"global.autoblock.block_duration":  "20m0s",
		"global.autoblock.state_path":      "/tmp/ab.json",
		"global.autoblock.whitelist":       "203.0.113.0/24\n198.51.100.10",
		"global.rate_limit.requests":       float64(600),
		"global.rate_limit.window":         "1m0s",
		"global.users.session_ttl":         float64(12),
		"global.trusted_proxies":           "10.0.0.0/8\n172.16.0.0/12",
		"global.watchdog.enabled":          true,
		"global.watchdog.interval":         "10s",
		"global.watchdog.failures":         float64(5),
		"global.watchdog.self_restart":     true,
	}
	for k, exp := range checks {
		if got[k] != exp {
			t.Errorf("%s: after PUT got %v (%T), want %v (%T)", k, got[k], got[k], exp, exp)
		}
	}
}
