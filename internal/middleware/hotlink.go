package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/pathmatch"
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
	refHost = strings.TrimPrefix(strings.ToLower(stripPort(refHost)), "www.")
	allowed = strings.TrimPrefix(strings.ToLower(stripPort(allowed)), "www.")
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

	// docs/SPECIFICATION.md lists extensions without the dot ("jpg"); compared
	// as a raw suffix that also protected "/camera.mjpg" and "/notjpg" (F13901).
	extMap := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		if ext = strings.ToLower(strings.TrimSpace(ext)); ext != "" {
			extMap[strings.TrimPrefix(ext, ".")] = true
		}
	}

	// A referer entry may be written as a URL, the way a Referer header is
	// ("https://partner.com/"); only its host identifies the site (F13900).
	var referers []string
	for _, ref := range allowedReferers {
		if host := refererEntryHost(ref); host != "" {
			referers = append(referers, host)
		}
	}
	allowedReferers = referers

	return func(w http.ResponseWriter, r *http.Request) bool {
		// A path parameter ("/a.jpg;x=1") hides the extension from a plain
		// suffix test while an upstream that strips parameters still serves
		// the file, so the path is judged both as sent and without them
		// (F13902).
		path := strings.ToLower(r.URL.Path)
		isProtected := hasProtectedExt(path, extMap)
		if !isProtected && strings.Contains(path, ";") {
			isProtected = hasProtectedExt(stripPathParams(path), extMap)
		}
		// "/a.jpg/." is served as "/a.jpg" but ends in "." (F63302).
		if !isProtected {
			clean := strings.ToLower(pathmatch.Clean(r.URL.Path))
			isProtected = hasProtectedExt(clean, extMap) ||
				(strings.Contains(clean, ";") && hasProtectedExt(stripPathParams(clean), extMap))
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
			// The Referer is a foreign site's address and may carry a token in its
			// query (a password-reset link that loaded this image); log it the way
			// the access log does (F14801).
			log.Warn("hotlink blocked", "path", clipLogField(r.URL.Path, maxLogURIBytes), "referer", logReferer(referer), "remote", r.RemoteAddr)
			http.Error(w, "403 Forbidden - hotlinking not allowed", http.StatusForbidden)
			return false
		}

		return true
	}
}

// hasProtectedExt reports whether the last path element ends in one of exts
// (lowercase, no leading dot).
func hasProtectedExt(path string, exts map[string]bool) bool {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for ext := range exts {
		// "."+ext so "tar.gz" works and "jpg" does not match "notjpg".
		if strings.HasSuffix(base, "."+ext) {
			return true
		}
	}
	return false
}

// stripPathParams removes ";param" suffixes from every path segment.
func stripPathParams(path string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if j := strings.IndexByte(seg, ';'); j >= 0 {
			segs[i] = seg[:j]
		}
	}
	return strings.Join(segs, "/")
}

// refererEntryHost reduces an allowed_referers entry to the host it names: a
// bare host ("partner.com"), a host:port, or a URL ("https://partner.com/x").
// An entry that names nothing returns "" so it cannot match a referer.
func refererEntryHost(entry string) string {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if entry == "" {
		return ""
	}
	if strings.Contains(entry, "://") {
		if u, err := url.Parse(entry); err == nil && u.Host != "" {
			return u.Host
		}
		return ""
	}
	if i := strings.IndexAny(entry, "/?#"); i >= 0 {
		entry = entry[:i]
	}
	return entry
}
