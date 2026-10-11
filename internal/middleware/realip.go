package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

type directIPContextKey struct{}

// RealIP extracts the real client IP from proxy headers.
// Checks X-Forwarded-For, X-Real-IP, CF-Connecting-IP.
// Uses rightmost untrusted IP from X-Forwarded-For for spoofing protection.
func RealIP(trustedProxies []string) Middleware {
	return RealIPDynamic(NewRealIPTrust(trustedProxies))
}

// RealIPTrust is a trusted-proxy set that can be replaced while the middleware
// is serving, so a config reload takes effect without a restart (F1990).
type RealIPTrust struct {
	nets atomic.Pointer[[]*net.IPNet]
}

// NewRealIPTrust parses cidrs into a trust set.
func NewRealIPTrust(cidrs []string) *RealIPTrust {
	t := &RealIPTrust{}
	t.Set(cidrs)
	return t
}

// Set atomically replaces the trusted proxy list.
func (t *RealIPTrust) Set(cidrs []string) {
	nets := parseCIDRs(cidrs)
	t.nets.Store(&nets)
}

// RealIPDynamic is RealIP reading its trusted proxies from t on every request.
func RealIPDynamic(t *RealIPTrust) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trusted := *t.nets.Load()
			if directIP := extractIP(r.RemoteAddr); directIP != nil {
				r = r.WithContext(context.WithValue(r.Context(), directIPContextKey{}, directIP.String()))
			}

			// When no trusted proxies are configured, skip all header
			// processing to avoid trusting spoofed proxy headers.
			if len(trusted) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Check whether the direct connection IP is a trusted proxy
			// before reading any proxy headers.
			directIP := extractIP(r.RemoteAddr)
			if directIP == nil || !isTrusted(directIP, trusted) {
				next.ServeHTTP(w, r)
				return
			}

			// Priority: CF-Connecting-IP > X-Real-IP > X-Forwarded-For.
			// Validate the header is a real, routable client IP before
			// trusting it: a trusted proxy forwarding an attacker-controlled,
			// non-IP (or otherwise bogus) value must not poison RemoteAddr —
			// that feeds ACLs, access logs, and BotGuard's loopback check
			// downstream. Loopback/unspecified values are rejected so a
			// client-supplied "127.0.0.1" can never buy loopback trust.
			// CF-Connecting-IP is only believed when it agrees with the
			// X-Forwarded-For hop the trusted proxy appended. Cloudflare sets
			// both to the same client; a non-Cloudflare trusted proxy that
			// appends XFF but forwards a client-set CF-Connecting-IP verbatim
			// would otherwise let the client pick its own address.
			xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
			if ip := r.Header.Get("CF-Connecting-IP"); acceptableForwardedIP(ip) &&
				(xff == "" || sameIP(ip, extractRealIP(xff, trusted))) {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
				next.ServeHTTP(w, r)
				return
			}

			// X-Real-IP is held to the same rule: a trusted proxy that only
			// appends to X-Forwarded-For forwards a client-set X-Real-IP
			// verbatim, so with XFF present the two must agree (F2740).
			if ip := r.Header.Get("X-Real-IP"); acceptableForwardedIP(ip) &&
				(xff == "" || sameIP(ip, extractRealIP(xff, trusted))) {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
				next.ServeHTTP(w, r)
				return
			}

			if xff != "" {
				ip := extractRealIP(xff, trusted)
				if acceptableForwardedIP(ip) {
					r.RemoteAddr = net.JoinHostPort(ip, "0")
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func DirectIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip, ok := r.Context().Value(directIPContextKey{}).(string); ok {
		return ip
	}
	return ""
}

// acceptableForwardedIP reports whether a proxy-forwarded value may be
// used as the client address: it must parse as an IP and must not be
// loopback or unspecified (those would grant localhost-only trust to a
// remote client if the fronting proxy forwards headers verbatim).
func acceptableForwardedIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && !parsed.IsLoopback() && !parsed.IsUnspecified()
}

// sameIP reports whether a and b parse to the same address.
func sameIP(a, b string) bool {
	pa, pb := net.ParseIP(a), net.ParseIP(b)
	return pa != nil && pb != nil && pa.Equal(pb)
}

// extractRealIP returns the rightmost untrusted IP from X-Forwarded-For.
func extractRealIP(xff string, trusted []*net.IPNet) string {
	parts := strings.Split(xff, ",")

	// Walk from right to left, find first untrusted IP. An entry that does
	// not parse even after stripping a port ("unknown", garbage) ends the
	// walk: everything left of it is client-supplied, so skipping it would
	// hand the client's own leftmost value back as the "real" address.
	for i := len(parts) - 1; i >= 0; i-- {
		ip := forwardedEntryIP(parts[i])
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return ""
		}
		if !isTrusted(parsed, trusted) {
			return ip
		}
	}

	// All IPs are trusted, return leftmost
	if len(parts) > 0 {
		return forwardedEntryIP(parts[0])
	}
	return ""
}

// forwardedEntryIP trims one X-Forwarded-For entry and drops a port some
// proxies append ("203.0.113.9:51234", "[2001:db8::9]:443").
func forwardedEntryIP(entry string) string {
	entry = strings.TrimSpace(entry)
	if net.ParseIP(entry) != nil {
		return entry
	}
	if host, _, err := net.SplitHostPort(entry); err == nil {
		return host
	}
	return entry
}

// extractIP parses the IP from an address that may include a port.
func extractIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// May be bare IP without port.
		return net.ParseIP(addr)
	}
	return net.ParseIP(host)
}

func isTrusted(ip net.IP, trusted []*net.IPNet) bool {
	for _, cidr := range trusted {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDRs(cidrs []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, s := range cidrs {
		if !strings.Contains(s, "/") {
			// Single IP → /32 or /128
			ip := net.ParseIP(s)
			if ip == nil {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				// Use the dotted form so an IPv4-mapped entry such as
				// "::ffff:1.2.3.4" becomes 1.2.3.4/32, not ::/32.
				s = ip4.String() + "/32"
			} else {
				s = s + "/128"
			}
		}
		_, cidr, err := net.ParseCIDR(s)
		if err == nil {
			// An IPv4-mapped network ("::ffff:1.2.3.0/120") is 16 bytes
			// long and never matches IPv4 clients; fold it to IPv4.
			if ones, bits := cidr.Mask.Size(); bits == 128 && ones >= 96 && cidr.IP.To4() != nil {
				cidr = &net.IPNet{IP: cidr.IP.To4(), Mask: net.CIDRMask(ones-96, 32)}
			}
			nets = append(nets, cidr)
		}
	}
	return nets
}
