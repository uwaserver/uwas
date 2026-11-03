package config

import (
	"strings"

	"golang.org/x/net/idna"
)

// CanonicalHost returns the form of a configured or requested host that every
// spelling of one site shares: lower case, no surrounding space, no
// absolute-form dot, and an internationalized name in its ASCII (punycode)
// form. Browsers send "xn--bcher-kva.example" on the wire for "bücher.example",
// so a host or alias written in Unicode must be compared in that form or it
// never matches a request and a duplicate check misses its punycode twin.
// A wildcard prefix and a port are kept. A name IDNA rejects, and any
// all-ASCII name, comes back lower-cased but otherwise unchanged.
func CanonicalHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	prefix := ""
	if strings.HasPrefix(h, "*.") {
		prefix, h = "*.", h[2:]
	}
	port := ""
	if i := strings.LastIndex(h, ":"); i != -1 && isASCII(h[i:]) && !strings.Contains(h[i:], "]") {
		h, port = h[:i], h[i:]
	}
	h = strings.TrimSuffix(h, ".")
	if isASCII(h) {
		return prefix + h + port
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return prefix + h + port
	}
	return prefix + strings.ToLower(ascii) + port
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
