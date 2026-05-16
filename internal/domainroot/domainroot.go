package domainroot

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
)

const appsScheme = "apps://"

// AppName reports the standalone app name targeted by a proxy domain.
func AppName(d config.Domain) (string, bool) {
	if config.DomainType(d.Type) != config.DomainTypeProxy {
		return "", false
	}
	for _, upstream := range d.Proxy.Upstreams {
		addr := strings.TrimSpace(upstream.Address)
		if !strings.HasPrefix(strings.ToLower(addr), appsScheme) {
			continue
		}
		name := strings.TrimSpace(addr[len(appsScheme):])
		if idx := strings.IndexAny(name, "/?#"); idx >= 0 {
			name = name[:idx]
		}
		if name == "" {
			return "", false
		}
		return name, true
	}
	return "", false
}

// ForDomain resolves the filesystem root operators should see for a domain.
// Static/PHP domains use Domain.Root. Proxy domains targeting apps://<name>
// use the standalone app WorkDir, which intentionally lives outside web_root.
func ForDomain(d config.Domain, store *apps.Store) (string, error) {
	appName, ok := AppName(d)
	if !ok {
		return d.Root, nil
	}
	if store == nil {
		return "", fmt.Errorf("app %q root unavailable: apps manager is not initialized", appName)
	}
	app, err := store.Get(appName)
	if err != nil {
		return "", fmt.Errorf("app %q root unavailable: %w", appName, err)
	}
	if app == nil {
		return "", fmt.Errorf("app %q root unavailable: app not found", appName)
	}
	if strings.TrimSpace(app.WorkDir) == "" {
		return "", fmt.Errorf("app %q root unavailable: work_dir is empty", appName)
	}
	return app.WorkDir, nil
}

func Fallback(globalWebRoot, domain string) string {
	if globalWebRoot == "" {
		return ""
	}
	return filepath.Join(globalWebRoot, domain, "public_html")
}
