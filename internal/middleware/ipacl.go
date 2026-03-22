package middleware

import (
	"net"
	"net/http"
)

// IPACLConfig holds the whitelist/blacklist configuration.
type IPACLConfig struct {
	Whitelist []string // CIDR ranges or single IPs to allow
	Blacklist []string // CIDR ranges or single IPs to deny
}

// IPACL returns middleware that checks client IP against allow/deny lists.
//
// Whitelist mode: if any whitelist entries are configured, only listed IPs
// are allowed (all others get 403).
//
// Blacklist mode: if only blacklist entries are configured, listed IPs are
// blocked (all others are allowed).
//
// If both are configured, whitelist takes precedence: the IP must be in the
// whitelist AND not in the blacklist.
func IPACL(cfg IPACLConfig) Middleware {
	whiteNets := parseCIDRs(cfg.Whitelist)
	blackNets := parseCIDRs(cfg.Blacklist)

	// No ACL rules configured — pass everything through.
	if len(whiteNets) == 0 && len(blackNets) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := aclClientIP(r)
			if ip == nil {
				// Cannot determine IP; deny by default when ACLs are active.
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}

			if len(whiteNets) > 0 {
				// Whitelist mode: must be in whitelist.
				if !ipInNets(ip, whiteNets) {
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
				// Also check blacklist if configured (whitelist + blacklist).
				if len(blackNets) > 0 && ipInNets(ip, blackNets) {
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Blacklist-only mode: deny if in blacklist.
			if ipInNets(ip, blackNets) {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ipInNets checks if an IP is contained in any of the given CIDR networks.
func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// aclClientIP extracts the client IP from the request's RemoteAddr.
func aclClientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Might be a bare IP without port.
		return net.ParseIP(r.RemoteAddr)
	}
	return net.ParseIP(host)
}
