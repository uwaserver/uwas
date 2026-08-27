package server

import (
	"strconv"
	"strings"
)

// resolveAppsUpstream translates an `apps://<name>` upstream address
// into the live 127.0.0.1:port the standalone apps supervisor has
// assigned to that app. For any other scheme (http://, https://,
// h2c://, grpc://, ...) the address is returned unchanged — the
// proxy pool consumes it directly.
//
// When the name doesn't resolve (no such app, or the app is currently
// stopped and ListenAddr returns ""), we fall back to a deterministic
// placeholder `http://127.0.0.1:0`. The connect attempt will fail
// fast with ECONNREFUSED and the existing proxy-error classifier
// renders a "no app running for <name>" diagnostic — much more
// informative than letting the operator's literal `apps://...` string
// hit url.Parse and 500 the request.
//
// Re-resolution: this runs at pool-build time (server boot + config
// reload). If a standalone app is started AFTER its proxy pool was
// built, the operator triggers re-resolution by saving the config
// (any reload will rebuild pools). A future improvement would be to
// register an apps.Manager hook that triggers proxy pool rebuild
// when a standalone app changes state, but config-reload covers the
// common cases.
func (s *Server) resolveAppsUpstream(addr string) string {
	const prefix = "apps://"
	if !strings.HasPrefix(addr, prefix) {
		return addr
	}
	target := strings.TrimPrefix(addr, prefix)
	// Strip any trailing path/query — `apps://name/foo?bar=1` is a
	// future hook for path-specific routing, but today we only honor
	// the name part and the proxy preserves the incoming request URI.
	if i := strings.IndexAny(target, "/?#"); i >= 0 {
		target = target[:i]
	}
	name, requestedPort := splitAppsTarget(target)
	if name == "" || s.appsMgr == nil {
		return "http://127.0.0.1:0"
	}
	listen := s.appsMgr.ListenAddrForPort(name, requestedPort)
	if listen == "" {
		if s.logger != nil {
			s.logger.Warn("proxy upstream apps:// unresolved (app stopped or unregistered)",
				"name", name, "address", addr)
		}
		return "http://127.0.0.1:0"
	}
	return "http://" + listen
}

func splitAppsTarget(target string) (string, int) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", 0
	}
	name := target
	port := 0
	if i := strings.LastIndex(target, ":"); i > 0 {
		candidate := strings.TrimSpace(target[i+1:])
		if parsed, err := strconv.Atoi(candidate); err == nil && parsed > 0 && parsed <= 65535 {
			name = target[:i]
			port = parsed
		}
	}
	return strings.TrimSpace(name), port
}

// startRegisteredApps launches every enabled app and then rebuilds the
// proxy pools that point at them.
//
// The rebuild is not optional. New() builds the pools while every app is
// still stopped, so ListenAddrForPort returns "" for each `apps://<name>`
// upstream and resolveAppsUpstream substitutes the http://127.0.0.1:0
// placeholder. StartAll() then launches the apps and assigns them real
// ports, but the pools already hold :0 and nothing re-resolves them —
// so every request to such a domain fails with ECONNREFUSED and the
// operator sees "502 Bad Gateway — upstream refused connection" for the
// entire process lifetime. The only escape was an unrelated config
// reload, which happens to call rebuildProxyPools.
//
// Rebuilding here is safe at boot: no listener is accepting yet, so
// ResetTransports has no in-flight traffic to disturb.
func (s *Server) startRegisteredApps() {
	if s.appsMgr == nil {
		return
	}
	s.appsMgr.StartAll()
	s.rebuildProxyPools(s.config.Domains)
}
