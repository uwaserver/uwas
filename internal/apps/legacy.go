package apps

import (
	"fmt"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

// MigrationReport summarizes the outcome of a one-shot legacy-to-v0.6
// app conversion. Returned by MigrateLegacy so callers can audit what
// got moved and what got rejected.
type MigrationReport struct {
	Migrated  []MigrationEntry `json:"migrated"`
	Skipped   []MigrationEntry `json:"skipped"`
	Conflicts []MigrationEntry `json:"conflicts"`
}

// MigrationEntry is a single per-domain row in the report.
type MigrationEntry struct {
	Domain  string `json:"domain"`
	AppName string `json:"app_name,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// MigrateLegacyDomains converts every `type=app` domain in `domains`
// into a standalone app entry (under /etc/uwas/apps.d/<name>.yaml)
// plus a `type=proxy` domain with `apps://<name>` upstream. The
// returned domains slice contains the in-memory representation the
// caller should write back to disk (via domain config persistence).
//
// Idempotent: if a standalone app already exists with the same name
// AND its command/port/env match the legacy config, we treat the
// domain as already-migrated and only convert the domain side.
// Conflicts (same app name, different config) are reported and left
// untouched so the operator can rename one or the other.
//
// `mgr` may be nil for dry-run conversion that doesn't persist
// anything — useful for migration preview UI.
func MigrateLegacyDomains(mgr *Manager, domains []config.Domain) (
	report *MigrationReport,
	updatedDomains []config.Domain,
	autoStart []string,
) {
	report = &MigrationReport{}
	updatedDomains = make([]config.Domain, len(domains))
	copy(updatedDomains, domains)

	for i, d := range domains {
		if d.Type != "app" {
			continue
		}

		appName := SanitizeName(d.Host)
		if appName == "" {
			report.Skipped = append(report.Skipped, MigrationEntry{
				Domain: d.Host,
				Reason: "could not derive a valid app name from host",
			})
			continue
		}

		newApp := buildAppFromLegacyDomain(d, appName)

		// Conflict check — only when a manager is provided.
		if mgr != nil {
			if existing, err := mgr.Store().Get(appName); err == nil && existing != nil {
				if looselyEqual(existing, newApp) {
					report.Migrated = append(report.Migrated, MigrationEntry{
						Domain:  d.Host,
						AppName: appName,
						Reason:  "standalone app already exists with matching config; converting domain only",
					})
					updatedDomains[i] = convertDomainToProxy(d, appName)
					continue
				}
				report.Conflicts = append(report.Conflicts, MigrationEntry{
					Domain:  d.Host,
					AppName: appName,
					Reason:  "standalone app with this name already exists with different config — rename one to proceed",
				})
				continue
			}

			if err := mgr.Register(newApp); err != nil {
				report.Skipped = append(report.Skipped, MigrationEntry{
					Domain:  d.Host,
					AppName: appName,
					Reason:  fmt.Sprintf("register failed: %v", err),
				})
				continue
			}
			if !newApp.Disabled {
				autoStart = append(autoStart, appName)
			}
		}

		report.Migrated = append(report.Migrated, MigrationEntry{
			Domain:  d.Host,
			AppName: appName,
		})
		updatedDomains[i] = convertDomainToProxy(d, appName)
	}
	return report, updatedDomains, autoStart
}

// buildAppFromLegacyDomain maps the legacy AppConfig fields to the
// v0.6 App schema. Preserves the domain's web root as the new
// workdir if the legacy config didn't specify one — the appmanager
// was implicitly using that path.
func buildAppFromLegacyDomain(d config.Domain, appName string) *App {
	a := &App{
		Name:        appName,
		Description: "Migrated from legacy domain " + d.Host,
		Runtime:     mapLegacyRuntime(d.App.Runtime),
		Command:     d.App.Command,
		WorkDir:     d.App.WorkDir,
		Port:        d.App.Port,
		Env:         cloneStringMap(d.App.Env),
		AutoRestart: d.App.AutoRestart || !d.App.Disabled,
		Disabled:    d.App.Disabled,
	}
	if a.WorkDir == "" {
		a.WorkDir = d.Root
	}
	return a
}

// convertDomainToProxy returns the type=proxy version of a legacy
// type=app domain. Preserves everything not tied to the in-process
// app (TLS, headers, cache, security, etc.).
func convertDomainToProxy(d config.Domain, appName string) config.Domain {
	d.Type = "proxy"
	d.App = config.AppConfig{}
	d.Proxy.Upstreams = []config.Upstream{{Address: "apps://" + appName, Weight: 1}}
	if d.Proxy.Algorithm == "" {
		d.Proxy.Algorithm = "round_robin"
	}
	return d
}

// mapLegacyRuntime translates the legacy free-text Runtime string
// into the typed apps.Runtime enum. Unknown values fall back to
// RuntimeCustom (matches the legacy appmanager fallback).
func mapLegacyRuntime(r string) Runtime {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "node", "nodejs", "node.js":
		return RuntimeNode
	case "python", "py":
		return RuntimePython
	case "ruby", "rb":
		return RuntimeRuby
	case "go", "golang":
		return RuntimeGo
	case "docker":
		return RuntimeDocker
	default:
		return RuntimeCustom
	}
}

// cloneStringMap defensively copies a map so the legacy domain and
// the new standalone app don't share backing storage.
func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// looselyEqual returns true when two App definitions describe the
// same workload for migration-idempotency purposes. Compares fields
// that materially affect spawning (runtime, command, port, workdir,
// env). Description / timestamps / docker spec ignored.
func looselyEqual(a, b *App) bool {
	if a.Runtime != b.Runtime || a.Command != b.Command || a.Port != b.Port {
		return false
	}
	if a.WorkDir != b.WorkDir {
		return false
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for k, v := range a.Env {
		if b.Env[k] != v {
			return false
		}
	}
	return true
}
