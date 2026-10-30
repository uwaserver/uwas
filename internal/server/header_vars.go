package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

func substituteHeaderVars(value string, r *http.Request) string {
	replacer := strings.NewReplacer(
		"$remote_addr", headerVarRemoteAddr(r),
		"$host", safeHeaderValue(r.Host),
		"$uri", safeHeaderValue(r.URL.RequestURI()),
		"$request_id", safeHeaderValue(r.Header.Get("X-Request-ID")),
	)
	return safeHeaderValue(replacer.Replace(value))
}

// reresolveHeaderTemplates re-evaluates the domain's templated response
// headers (those using $remote_addr, $uri, ...) for the current request. A
// cache hit copies the stored headers over the response, and a stored value
// holds whatever the visitor who filled the cache resolved it to; without
// this every later visitor was shown that visitor's address (F39302). Only
// headers the response already carries are touched, so a header the handler
// removed or never emitted stays that way.
func reresolveHeaderTemplates(w http.Header, r *http.Request, domain *config.Domain) {
	for _, m := range []map[string]string{domain.Headers.Add, domain.Headers.ResponseAdd} {
		for k, v := range m {
			if !strings.Contains(v, "$") {
				continue
			}
			if _, ok := w[http.CanonicalHeaderKey(k)]; ok {
				w.Set(k, substituteHeaderVars(v, r))
			}
		}
	}
}

func headerVarRemoteAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return safeHeaderValue(host)
	}
	return safeHeaderValue(r.RemoteAddr)
}

func safeHeaderValue(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
}
