package admin

import (
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
)

// handleAppsMigrate is the manual trigger for the legacy
// type=app → apps migration. v0.6+ runs the same conversion
// automatically at boot, so this endpoint exists mainly for:
//
//   - dry-run preview (?dry_run=true) — see what WOULD be converted
//     without changing anything
//   - reruns after the operator manually edits domain configs
//
// All actual conversion logic lives in apps.MigrateLegacyDomains;
// this handler just wraps it with HTTP plumbing + domain persistence.
func (s *Server) handleAppsMigrate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	dryRun := r.URL.Query().Get("dry_run") == "true"

	s.configMu.RLock()
	domains := make([]config.Domain, len(s.config.Domains))
	copy(domains, s.config.Domains)
	s.configMu.RUnlock()

	// Pass nil manager for dry-run so MigrateLegacyDomains computes
	// the plan without touching disk or registering apps.
	mgr := s.appsMgr
	if dryRun {
		mgr = nil
	}

	report, updated, autoStart := apps.MigrateLegacyDomains(mgr, domains)

	if dryRun {
		jsonResponse(w, map[string]any{
			"dry_run": true,
			"report":  report,
		})
		return
	}

	// Persist converted domains.
	for _, d := range updated {
		if d.Type != "proxy" || len(d.Proxy.Upstreams) == 0 {
			continue
		}
		if len(d.Proxy.Upstreams[0].Address) < 7 ||
			d.Proxy.Upstreams[0].Address[:7] != "apps://" {
			continue
		}
		path, err := s.domainFilePath(d.Host)
		if err != nil {
			continue
		}
		data, err := yaml.Marshal(d)
		if err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		_ = atomicWriteFile(path, data, 0600)
	}

	// Refresh in-memory config so the next request routes via proxy.
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

	// Start migrated apps that weren't disabled.
	for _, name := range autoStart {
		_ = s.appsMgr.Start(name)
	}

	s.recordAuditR(r, "app.migrate",
		"migrated="+itoaPos(len(report.Migrated))+
			" skipped="+itoaPos(len(report.Skipped))+
			" conflicts="+itoaPos(len(report.Conflicts)),
		true)

	s.maybeReloadForApps()

	jsonResponse(w, map[string]any{"dry_run": false, "report": report})
}

// itoaPos formats a non-negative int as a string without allocating
// via strconv.Itoa's small-int interning path. Used in the audit log
// formatter to keep the line concise.
func itoaPos(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
