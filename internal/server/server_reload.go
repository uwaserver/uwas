package server

import (
	"fmt"

	"github.com/uwaserver/uwas/internal/config"
)

// reload re-reads and applies the config file.
func (s *Server) reload() error {
	if s.configPath == "" {
		return fmt.Errorf("no config path set")
	}

	newCfg, err := config.Load(s.configPath)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	// global.log_level was read once at startup and never again: a reload
	// accepted a new value, wrote it to the config and showed it in the panel
	// while the process kept logging at the old threshold until restarted.
	s.logger.SetLevel(newCfg.Global.LogLevel)

	// The middleware chain is built once at startup, so the request-log flag
	// has to be swapped here rather than re-captured. Without this a panel
	// toggle would write the new value to disk and keep logging regardless.
	s.requestLog.Store(newCfg.Global.AccessLog.RequestLogEnabled())

	// Update vhosts
	s.vhosts.Update(newCfg.Domains)

	// Refresh the autoblock whitelist. Thresholds and durations still need a
	// restart — they are captured when the counters are built — but the
	// whitelist must track a reload, because that is where a freshly synced
	// set of Cloudflare edge ranges arrives. Blocking one of those takes the
	// site offline for everyone that edge serves, so it cannot wait.
	if s.autoblocker.Enabled() {
		s.autoblocker.SetWhitelist(autoBlockWhitelist(newCfg))
		s.autoblocker.SetFirewallSync(newCfg.Global.AutoBlock.FirewallSync)
	}

	// Update TLS domains
	s.tlsMgr.UpdateDomains(newCfg.Domains)

	// Rebuild every per-domain routing map (guards, rate limiters, rewrite
	// engines, image-opt chains) and swap them in. Shared with onDomainChange
	// so a per-domain security change takes effect the same way through either
	// path.
	s.rebuildDomainRouting(newCfg.Domains, newCfg.Global.TrustedProxies)

	// Rebuild proxy pools + balancers + health checkers against the new
	// config. Factored into rebuildProxyPools so onDomainChange (which
	// doesn't go through reload — it operates on in-memory state) can
	// reuse the same logic.
	s.rebuildProxyPools(newCfg.Domains)

	// Update bandwidth manager with new domain configs
	if s.bwMgr != nil {
		s.bwMgr.UpdateDomains(newCfg.Domains)
	}

	// Update webhook configs
	if s.webhookMgr != nil {
		s.webhookMgr.UpdateWebhooks(toWebhookConfigs(newCfg.Global.Webhooks))
	}

	// Update health monitor domains
	if s.monitor != nil {
		s.monitor.UpdateDomains(newCfg.Domains)
	}

	// Apps refresh — pick up any new YAML files in /etc/uwas/apps.d/
	// and start every enabled app that is not already running. Existing
	// running apps are left untouched; command/port changes still take
	// effect on an explicit Restart (the LoadAll contract).
	if s.appsMgr != nil {
		if _, _, err := s.appsMgr.LoadAll(); err != nil {
			s.logger.Warn("apps: reload failed", "error", err)
		} else {
			s.appsMgr.StartAll()
		}
	}

	// Update stored config IN PLACE under write lock. The admin server
	// was constructed with a *config.Config pointer that it dereferences
	// for every read; if we swapped the pointer here (s.config = newCfg)
	// admin would keep reading the stale config, which means subsequent
	// domain CRUDs through the admin API would mutate a config no other
	// subsystem references and the vhost router would never see them —
	// every request to a freshly-created domain would 421.
	s.configMu.Lock()
	*s.config = *newCfg
	s.configMu.Unlock()

	s.logger.Info("config reloaded", "domains", len(newCfg.Domains))
	return nil
}
