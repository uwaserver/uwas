package server

import (
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/domainutil"
)

// canonicalHostname returns the hostname reqHost should resolve to under the
// domain's canonical_host preference, and whether that differs from reqHost.
//
// canonical_host ("apex" or "www", the panel's "primary URL") was stored,
// validated and displayed but never read on the request path — so choosing www
// as primary never redirected the apex, and both hostnames answered 200
// (duplicate content). Only an explicit preference on a host with a real
// apex/www duality applies; an unset preference leaves both hostnames reachable
// exactly as before.
func canonicalHostname(domain *config.Domain, reqHost string) (string, bool) {
	pref := strings.ToLower(strings.TrimSpace(domain.CanonicalHost))
	if pref != "www" && pref != "apex" {
		return reqHost, false
	}

	host := reqHost
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i] // strip :port (bracketed IPv6 has no bare port here)
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))

	apex, www, ok := domainutil.ApexAndWWWHost(host)
	if !ok {
		return reqHost, false
	}

	target := apex
	if pref == "www" {
		target = www
	}
	if target == "" || target == host {
		return reqHost, false // already canonical — no change, no loop
	}
	return target, true
}

// canonicalRedirectLocation returns the absolute URL to 301 to when the request
// arrived on the non-canonical hostname, preserving path and query, or "".
func canonicalRedirectLocation(domain *config.Domain, r *http.Request) string {
	target, ok := canonicalHostname(domain, r.Host)
	if !ok {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + target + r.URL.RequestURI()
}
