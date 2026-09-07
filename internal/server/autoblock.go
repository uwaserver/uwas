package server

import (
	"net"

	"github.com/uwaserver/uwas/internal/autoblock"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/firewall"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/serverip"
	"github.com/uwaserver/uwas/internal/watchdog"
)

// newAutoBlocker builds the blocker from config, wiring in the firewall hooks
// and the whitelist that keeps it from blocking infrastructure.
func newAutoBlocker(cfg *config.Config, log *logger.Logger) *autoblock.Blocker {
	ab := cfg.Global.AutoBlock
	b := autoblock.New(autoblock.Config{
		Enabled:          ab.Enabled,
		Window:           ab.Window.Duration,
		MaxConnections:   ab.MaxConnections,
		MaxAborts:        ab.MaxAborts,
		MaxConcurrent:    ab.MaxConcurrent,
		MaxWAFHits:       ab.MaxWAFHits,
		MaxRateHits:      ab.MaxRateHits,
		MaxNotFound:      ab.MaxNotFound,
		BlockDuration:    ab.BlockDuration.Duration,
		MaxBlockDuration: ab.MaxBlockDuration.Duration,
		Escalate:         ab.Escalate,
		DryRun:           ab.DryRun,
		FirewallSync:     ab.FirewallSync,
		Whitelist:        autoBlockWhitelist(cfg),
		PersistPath:      ab.StatePath,
	}, log)
	b.SetFirewall(firewall.BlockIP, firewall.UnblockIP)

	if ab.Enabled {
		log.Info("autoblock enabled",
			"window", ab.Window.Duration.String(),
			"block_duration", ab.BlockDuration.Duration.String(),
			"firewall_sync", ab.FirewallSync,
			"dry_run", ab.DryRun,
		)
		if cfg.Global.ProxyProtocol {
			// Under PROXY protocol every connection arrives from the load
			// balancer, so the connection counters would all point at it.
			// The listener guard is skipped and only request-layer signals
			// (which run after RealIP has resolved the true client) apply.
			log.Warn("autoblock: proxy_protocol is on, so connection-level detection is disabled; " +
				"only WAF/rate/404 signals apply. A TLS handshake flood must be stopped at the load balancer.")
		}
	}
	return b
}

// autoBlockWhitelist assembles the never-block set from the operator's own
// list plus everything blocking would break: the reverse proxies we trust, the
// Cloudflare edge ranges, and this host's own addresses.
//
// The Cloudflare part is not optional. Behind a CDN every connection carries
// an edge IP; a busy edge trips a connection threshold in seconds, and
// blocking it takes the site offline for every visitor that edge serves.
func autoBlockWhitelist(cfg *config.Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}

	for _, v := range cfg.Global.AutoBlock.Whitelist {
		add(v)
	}
	for _, v := range cfg.Global.TrustedProxies {
		add(v)
	}
	for _, v := range cfg.Global.Cloudflare.IPRanges {
		add(v)
	}
	for _, info := range serverip.DetectAll() {
		add(info.IP)
	}
	return out
}

// guardListener wraps a listener with connection-level autoblocking, unless
// PROXY protocol makes the peer address meaningless.
func (s *Server) guardListener(ln net.Listener) net.Listener {
	if s.autoblocker == nil || !s.autoblocker.Enabled() || s.config.Global.ProxyProtocol {
		return ln
	}
	return newGuardListener(ln, s.autoblocker)
}

// AutoBlocker exposes the blocker for the admin API and reload path.
func (s *Server) AutoBlocker() *autoblock.Blocker { return s.autoblocker }

// newWatchdog picks a probe target from the configured listeners. Plain HTTP
// is preferred: it exercises the same accept path with none of the TLS
// handshake cost, so the probe stays cheap on a server that is already
// struggling. HTTPS is the fallback for an HTTPS-only deployment.
func newWatchdog(cfg *config.Config, log *logger.Logger) *watchdog.Watchdog {
	wc := cfg.Global.Watchdog

	addr := watchdog.ProbeAddr(cfg.Global.HTTPListen)
	useTLS := false
	if addr == "" {
		addr = watchdog.ProbeAddr(cfg.Global.HTTPSListen)
		useTLS = true
	}
	if addr == "" {
		addr = watchdog.ProbeAddr(cfg.Global.Admin.Listen)
		useTLS = true
	}

	return watchdog.New(watchdog.Config{
		Enabled:     wc.Enabled && addr != "",
		Interval:    wc.Interval.Duration,
		Timeout:     wc.Timeout.Duration,
		Failures:    wc.Failures,
		SelfRestart: wc.SelfRestart,
	}, addr, useTLS, log)
}
