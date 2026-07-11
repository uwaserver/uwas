package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type directIPContextKey struct{}

// RealIP extracts the real client IP from proxy headers.
// Checks X-Forwarded-For, X-Real-IP, CF-Connecting-IP.
// Uses rightmost untrusted IP from X-Forwarded-For for spoofing protection.
func RealIP(trustedProxies []string) Middleware {
	trusted := parseCIDRs(trustedProxies)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			// Validate the header is a real IP before trusting it: a trusted
			// proxy forwarding an attacker-controlled, non-IP (or otherwise
			// bogus) value must not poison RemoteAddr — that feeds ACLs, access
			// logs, and BotGuard's loopback check downstream.
			if ip := r.Header.Get("CF-Connecting-IP"); ip != "" && net.ParseIP(ip) != nil {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
				next.ServeHTTP(w, r)
				return
			}

			if ip := r.Header.Get("X-Real-IP"); ip != "" && net.ParseIP(ip) != nil {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
				next.ServeHTTP(w, r)
				return
			}

			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				ip := extractRealIP(xff, trusted)
				if ip != "" {
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

// extractRealIP returns the rightmost untrusted IP from X-Forwarded-For.
func extractRealIP(xff string, trusted []*net.IPNet) string {
	parts := strings.Split(xff, ",")

	// Walk from right to left, find first untrusted IP
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		parsed := net.ParseIP(ip)
		if parsed == nil {
			continue
		}
		if !isTrusted(parsed, trusted) {
			return ip
		}
	}

	// All IPs are trusted, return leftmost
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}
	return ""
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
			if ip.To4() != nil {
				s = s + "/32"
			} else {
				s = s + "/128"
			}
		}
		_, cidr, err := net.ParseCIDR(s)
		if err == nil {
			nets = append(nets, cidr)
		}
	}
	return nets
}
