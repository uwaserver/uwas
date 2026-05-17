package server

import (
	"net"
	"net/http"
	"strings"
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
