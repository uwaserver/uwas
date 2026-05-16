package admin

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
)

// MigrateLegacyAppsAtBoot scans the in-memory domain config for any
// `type=app` entries left over from pre-v0.6 installs, converts each
// to a standalone app + `type=proxy` domain via apps.MigrateLegacyDomains,
// and persists the rewritten domain YAML to disk.
//
// Called once during server boot AFTER appsMgr.LoadAll() but BEFORE
// appsMgr.StartAll() so the migrated apps come up on the same boot
// without an operator having to click anything.
//
// Idempotent: re-running on an already-migrated config finds zero
// `type=app` entries and is a no-op.
//
// Returns the count of converted domains so the caller can log a
// concise "migrated N legacy apps" line.
func (s *Server) MigrateLegacyAppsAtBoot() int {
	if s.appsMgr == nil {
		return 0
	}

	s.configMu.RLock()
	domains := make([]config.Domain, len(s.config.Domains))
	copy(domains, s.config.Domains)
	s.configMu.RUnlock()

	// Quick exit if nothing to do — common case once the deployment
	// has been on v0.6+ for a while.
	hasLegacy := false
	for _, d := range domains {
		if d.Type == "app" {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return 0
	}

	if s.logger != nil {
		s.logger.Info("apps: scanning for legacy type=app domains to auto-migrate")
	}

	report, updated, _ := apps.MigrateLegacyDomains(s.appsMgr, domains)

	count := len(report.Migrated)
	if count == 0 {
		// All legacy entries were conflicts or skipped — log details
		// so the operator knows why nothing moved.
		for _, e := range report.Conflicts {
			if s.logger != nil {
				s.logger.Warn("apps: boot-migration conflict", "domain", e.Domain, "reason", e.Reason)
			}
		}
		for _, e := range report.Skipped {
			if s.logger != nil {
				s.logger.Warn("apps: boot-migration skipped", "domain", e.Domain, "reason", e.Reason)
			}
		}
		return 0
	}

	// Write each converted domain back to disk. The in-memory config
	// is the source of truth for the runtime, but operators expect
	// the YAML files to reflect the new shape so the next config
	// reload doesn't undo the migration.
	for _, d := range updated {
		if d.Type != "proxy" {
			// Only the migrated entries need rewriting; everything
			// else came back unchanged from MigrateLegacyDomains.
			continue
		}
		// Detect "this was a legacy entry I just converted" by
		// checking the upstream — we set it to apps://<name>.
		if len(d.Proxy.Upstreams) == 0 ||
			len(d.Proxy.Upstreams[0].Address) < len("apps://") ||
			d.Proxy.Upstreams[0].Address[:7] != "apps://" {
			continue
		}

		path, err := s.domainFilePath(d.Host)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("apps: boot-migration could not resolve domain path",
					"domain", d.Host, "error", err)
			}
			continue
		}
		data, err := yaml.Marshal(d)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("apps: boot-migration marshal failed", "domain", d.Host, "error", err)
			}
			continue
		}
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		if err := atomicWriteFile(path, data, 0600); err != nil {
			if s.logger != nil {
				s.logger.Warn("apps: boot-migration write failed", "domain", d.Host, "error", err)
			}
			continue
		}
	}

	// Update the in-memory config so request routing uses type=proxy
	// (not type=app) for the migrated hosts on the next request.
	s.configMu.Lock()
	for i := range s.config.Domains {
		for _, u := range updated {
			if s.config.Domains[i].Host == u.Host {
				s.config.Domains[i] = u
				break
			}
		}
	}
	s.configMu.Unlock()

	// Note: we DON'T start the migrated apps here — the caller
	// (server.New → s.appsMgr.StartAll()) handles that uniformly for
	// every registered app. Starting here would race StartAll and
	// surface "already running" errors.

	if s.logger != nil {
		s.logger.Info("apps: boot-migration complete",
			"migrated", count,
			"skipped", len(report.Skipped),
			"conflicts", len(report.Conflicts))
	}
	return count
}
