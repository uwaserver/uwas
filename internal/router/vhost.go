package router

import (
	"sort"
	"strings"
	"sync/atomic"

	"github.com/uwaserver/uwas/internal/config"
)

// vhostMap holds the read-mostly routing data. Swapped atomically on reload.
type vhostMap struct {
	exact     map[string]*config.Domain // exact host → domain
	wildcards []wildcardEntry           // sorted by length desc (longest match first)
	fallback  *config.Domain            // default if nothing matches
}

// VHostRouter routes incoming requests to domain configurations based on Host header.
type VHostRouter struct {
	// current is swapped atomically on reload — readers see either old or new, never partial.
	current atomic.Pointer[vhostMap]
}

type wildcardEntry struct {
	suffix string // e.g. ".example.com" for "*.example.com"
	domain *config.Domain
}

func NewVHostRouter(domains []config.Domain) *VHostRouter {
	r := &VHostRouter{}
	r.store(domains)
	return r
}

// store builds a new vhostMap and atomically swaps it.
func (r *VHostRouter) store(domains []config.Domain) {
	exact := make(map[string]*config.Domain, len(domains)*2)
	var wildcards []wildcardEntry
	var fallback *config.Domain

	for i := range domains {
		d := &domains[i]
		host := strings.ToLower(d.Host)

		if strings.HasPrefix(host, "*.") {
			suffix := host[1:] // "*.example.com" → ".example.com"
			wildcards = append(wildcards, wildcardEntry{suffix: suffix, domain: d})
		} else {
			registerExactHost(exact, host, d)
		}

		// Register aliases
		for _, alias := range d.Aliases {
			alias = strings.ToLower(alias)
			if strings.HasPrefix(alias, "*.") {
				suffix := alias[1:]
				wildcards = append(wildcards, wildcardEntry{suffix: suffix, domain: d})
			} else {
				registerExactHost(exact, alias, d)
			}
		}

		// First domain is fallback
		if fallback == nil {
			fallback = d
		}
	}

	// Sort wildcards by suffix length descending (longest match first)
	sort.Slice(wildcards, func(i, j int) bool {
		return len(wildcards[i].suffix) > len(wildcards[j].suffix)
	})

	m := &vhostMap{
		exact:     exact,
		wildcards: wildcards,
		fallback:  fallback,
	}
	r.current.Store(m)
}

func registerExactHost(exact map[string]*config.Domain, host string, d *config.Domain) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return
	}
	exact[host] = d
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		exact[host[:idx]] = d
		return
	}
	// Implicit www.↔apex variants: only fill them when no domain explicitly
	// owns the key. An implicit/derived registration must never clobber
	// another domain's explicit host — otherwise registering www.example.com
	// would hijack a different tenant's explicit example.com (last-writer-wins,
	// order-dependent).
	if strings.HasPrefix(host, "www.") {
		apex := strings.TrimPrefix(host, "www.")
		if apex != "" && strings.Contains(apex, ".") {
			if _, exists := exact[apex]; !exists {
				exact[apex] = d
			}
		}
		return
	}
	if !strings.HasPrefix(host, "*.") && strings.Contains(host, ".") {
		if _, exists := exact["www."+host]; !exists {
			exact["www."+host] = d
		}
	}
}

// Lookup finds the domain config for a given host. Wrapper over
// LookupWithStatus for callers that don't need the configured-vs-fallback
// signal.
func (r *VHostRouter) Lookup(host string) *config.Domain {
	d, _ := r.LookupWithStatus(host)
	return d
}

// LookupWithStatus finds the domain config and reports whether the match was
// against a real configured host/wildcard (true) versus the default fallback
// (false). One pass over the map + wildcard list instead of two — used by the
// HTTP entry path which needed both pieces of information per request
// (was P10).
func (r *VHostRouter) LookupWithStatus(host string) (*config.Domain, bool) {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)

	m := r.current.Load()

	if d, ok := m.exact[host]; ok {
		return d, true
	}
	for _, wc := range m.wildcards {
		if strings.HasSuffix(host, wc.suffix) {
			return wc.domain, true
		}
	}
	return m.fallback, false
}

// IsConfigured returns true if the host matches a configured domain (exact or
// wildcard), as opposed to falling through to the default fallback. Thin
// wrapper over LookupWithStatus; kept for back-compat with existing callers.
func (r *VHostRouter) IsConfigured(host string) bool {
	_, ok := r.LookupWithStatus(host)
	return ok
}

// Update replaces all domain configurations (hot reload).
func (r *VHostRouter) Update(domains []config.Domain) {
	r.store(domains)
}
