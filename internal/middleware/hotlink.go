package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/uwaserver/uwas/internal/logger"
)

// isAllowedReferer checks whether the referer URL (lowercased) is allowed under
// the given allowed domain. It uses domain-suffix matching: example.com allows
// example.com and sub.example.com, but NOT example.com.evil.com or notexample.com.
// This prevents the substring-bypass (strings.Contains) that allowed evil sites
// like https://example.com.evil.com/ to pass a guard configured for example.com.
func isAllowedReferer(refLower, allowed string) bool {
	// Try parsing as a full URL first (e.g. "https://example.com/page")
	if u, err := url.Parse(refLower); err == nil && u.Host != "" {
		refHost := strings.ToLower(u.Host)
		return domainSuffixMatch(refHost, allowed)
	}
	// Fallback: compare as plain host strings
	return domainSuffixMatch(refLower, allowed)
}

// domainSuffixMatch returns true if refHost ends with "."+allowed or equals allowed.
// example.com matches: "example.com", "sub.example.com"
// example.com does NOT match: "example.com.evil.com", "notexample.com"
func domainSuffixMatch(refHost, allowed string) bool {
	refHost = strings.ToLower(strings.TrimPrefix(stripPort(refHost), "www."))
	allowed = strings.ToLower(strings.TrimPrefix(stripPort(allowed), "www."))
	if refHost == allowed {
		return true
	}
	return strings.HasSuffix(refHost, "."+allowed)
}

// stripPort removes an optional ":port" suffix so a host:port authority is
// compared as a bare domain. The port is not part of the domain identity, but
// it arrives attached at both ends of the comparison: a parsed referer
// exposes it through url.URL.Host ("example.com:8443"), and the same-host
// allowance passes r.Host, which also carries it on a non-default-port site.
// Comparing the two with the port still attached made neither the exact
// domain nor the "."+allowed suffix match, so legitimate referers were 403'd.
func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// HotlinkGuard blocks direct linking to resources from unauthorized referers.
// It returns true when the request should proceed.
func HotlinkGuard(log *logger.Logger, allowedReferers []string, extensions []string) func(http.ResponseWriter, *http.Request) bool {
	if len(extensions) == 0 {
		extensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg", ".mp4", ".webm", ".mp3", ".zip", ".pdf"}
	}

	extMap := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extMap[strings.ToLower(ext)] = true
	}

	return func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.ToLower(r.URL.Path)
		isProtected := false
		for ext := range extMap {
			if strings.HasSuffix(path, ext) {
				isProtected = true
				break
			}
		}

		if !isProtected {
			return true
		}

		referer := r.Referer()
		if referer == "" {
			return true
		}

		refLower := strings.ToLower(referer)
		allowed := false
		for _, ref := range allowedReferers {
			// Parse the referer URL to extract its hostname, then use domain-suffix
			// matching instead of substring containment. This blocks domain-suffix attacks
			// (e.g. https://example.com.evil.com/) that would pass strings.Contains.
			if isAllowedReferer(refLower, ref) {
				allowed = true
				break
			}
		}

		// Also allow if the referer is from the same host as the request.
		if !allowed {
			host := strings.ToLower(r.Host)
			if isAllowedReferer(refLower, host) {
				allowed = true
			}
		}

		if !allowed {
			log.Warn("hotlink blocked", "path", r.URL.Path, "referer", referer, "remote", r.RemoteAddr)
			http.Error(w, "403 Forbidden - hotlinking not allowed", http.StatusForbidden)
			return false
		}

		return true
	}
}
