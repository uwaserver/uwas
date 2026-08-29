package admin

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/handler/static"
)

// handleDomainCacheControl answers "what Cache-Control would this path get,
// and which setting decided it?"
//
// Four settings can decide it — locations, domain headers, cache rules and
// browser_cache — and they do not win in the order they appear in the config.
// A cache rule quietly overrides a location; a page under an immutable_paths
// prefix still revalidates. None of that is visible from a settings form, so
// the panel asks the server rather than guessing.
func (s *Server) handleDomainCacheControl(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	host := r.PathValue("host")
	if !s.canAccessDomain(r, host) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	s.configMu.RLock()
	defer s.configMu.RUnlock()

	for i := range s.config.Domains {
		d := &s.config.Domains[i]
		if d.Host != host {
			continue
		}
		decision := static.ResolveCacheControl(d, path,
			s.config.Global.Cache.Enabled && d.Cache.Enabled)
		jsonResponse(w, map[string]any{
			"host":   host,
			"path":   path,
			"value":  decision.Value,
			"source": decision.Source,
			"detail": decision.Detail,
		})
		return
	}
	jsonError(w, "domain not found", http.StatusNotFound)
}
