package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// RewriteBackendLocation points a Location header that names the backend
// itself at the client-facing scheme and host.
//
// The proxy sends the backend's own address as Host, so a backend that builds
// an absolute redirect from the Host it received (Apache mod_dir, trailing-slash
// redirects, framework redirect helpers) answers with an address the visitor
// cannot reach and that discloses the internal topology. Like nginx's default
// proxy_redirect, only a Location whose authority equals the backend's is
// rewritten; relative and third-party URLs are the backend's to choose.
// Path, query and fragment are carried over byte for byte (F40302).
func RewriteBackendLocation(h http.Header, backend *url.URL, scheme, host string) {
	loc := h.Get("Location")
	if loc == "" || backend == nil || host == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return
	}
	if !strings.EqualFold(u.Hostname(), backend.Hostname()) || effectivePort(u) != effectivePort(backend) {
		return
	}
	// u.Scheme is lower-cased by the parser, so the authority starts after the
	// first "://" of the raw value; keep everything after it untouched.
	i := strings.Index(loc, "://")
	if i < 0 {
		return
	}
	rest := loc[i+3:]
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		rest = rest[end:]
	} else {
		rest = ""
	}
	h.Set("Location", scheme+"://"+host+rest)
}

// effectivePort is the URL's port, or the scheme's default when it has none.
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}
