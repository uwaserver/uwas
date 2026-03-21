package router

import (
	"sort"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/config"
)

// VHostRouter routes incoming requests to domain configurations based on Host header.
type VHostRouter struct {
	mu        sync.RWMutex
	exact     map[string]*config.Domain // exact host → domain
	wildcards []wildcardEntry           // sorted by length desc (longest match first)
	fallback  *config.Domain            // default if nothing matches
}

type wildcardEntry struct {
	suffix string         // e.g. ".example.com" for "*.example.com"
	domain *config.Domain
}

func NewVHostRouter(domains []config.Domain) *VHostRouter {
	r := &VHostRouter{
		exact: make(map[string]*config.Domain),
	}
	r.load(domains)
	return r
}

func (r *VHostRouter) load(domains []config.Domain) {
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
			exact[host] = d
		}

		// Register aliases
		for _, alias := range d.Aliases {
			alias = strings.ToLower(alias)
			if strings.HasPrefix(alias, "*.") {
				suffix := alias[1:]
				wildcards = append(wildcards, wildcardEntry{suffix: suffix, domain: d})
			} else {
				exact[alias] = d
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

	r.mu.Lock()
	r.exact = exact
	r.wildcards = wildcards
	r.fallback = fallback
	r.mu.Unlock()
}

// Lookup finds the domain config for a given host.
func (r *VHostRouter) Lookup(host string) *config.Domain {
	// Strip port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Exact match
	if d, ok := r.exact[host]; ok {
		return d
	}

	// 2. Wildcard match (longest suffix first)
	for _, wc := range r.wildcards {
		if strings.HasSuffix(host, wc.suffix) {
			return wc.domain
		}
	}

	// 3. Default fallback
	return r.fallback
}

// Update replaces all domain configurations (hot reload).
func (r *VHostRouter) Update(domains []config.Domain) {
	r.load(domains)
}

// Domains returns the number of configured exact hosts.
func (r *VHostRouter) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.exact)
}
