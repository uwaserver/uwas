package domainroot

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
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
		if host, _, ok := strings.Cut(name, ":"); ok {
			name = host
		}
		if name == "" {
			return "", false
		}
		return name, true
	}
	return "", false
}

// LocalAppName reports the app name targeted by older proxy domains that
// persisted the resolved loopback port instead of the stable apps:// name.
func LocalAppName(d config.Domain, instances []apps.Instance) (string, bool) {
	if config.DomainType(d.Type) != config.DomainTypeProxy {
		return "", false
	}
	for _, upstream := range d.Proxy.Upstreams {
		port, ok := localUpstreamPort(upstream.Address)
		if !ok {
			continue
		}
		for _, inst := range instances {
			if inst.Port == port && strings.TrimSpace(inst.WorkDir) != "" {
				return inst.Name, true
			}
		}
	}
	return "", false
}

// ForDomain resolves the filesystem root operators should see for a domain.
// Static/PHP domains use Domain.Root. Proxy domains targeting apps://<name>
// use the standalone app WorkDir, which intentionally lives outside web_root.
func ForDomain(d config.Domain, store *apps.Store) (string, error) {
	return ForDomainWithApps(d, store, nil)
}

// ForDomainWithApps also handles legacy app proxy domains that point at the
// app's resolved loopback port, such as http://127.0.0.1:3001.
func ForDomainWithApps(d config.Domain, store *apps.Store, instances []apps.Instance) (string, error) {
	appName, ok := AppName(d)
	if !ok {
		appName, ok = LocalAppName(d, instances)
	}
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

func localUpstreamPort(addr string) (int, bool) {
	addr = config.NormalizeProxyUpstreamAddress(addr)
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return 0, false
	}
	host, portText, err := net.SplitHostPort(u.Host)
	if err != nil || portText == "" {
		return 0, false
	}
	if !isLoopbackHost(host) {
		return 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func Fallback(globalWebRoot, domain string) string {
	if globalWebRoot == "" {
		return ""
	}
	return filepath.Join(globalWebRoot, domain, "public_html")
}
