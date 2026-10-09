package server

import (
	"crypto/tls"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

// clientCertRefusal reports the status to refuse a request routed to domain
// with, when the connection did not pass that domain's client-certificate
// policy, or 0 when it may be served.
//
// The TLS manager applies ssl.client_ca per SNI name, but routing follows the
// Host header. A client could handshake as a public domain, or with no SNI,
// and then ask for the mTLS domain — getting it without ever being asked for
// a certificate. 421 makes a coalescing HTTP/2 client retry on a connection
// whose SNI names this domain. A "require" domain is also never served
// without a verified chain, including over plain HTTP while its certificate
// is still pending.
//
// ssl.min_version has the same gap: the version is negotiated under the SNI
// name's policy, so a TLS 1.2 handshake as a public domain must not reach a
// "1.3" domain through Host.
func (s *Server) clientCertRefusal(domain *config.Domain, r *http.Request) int {
	if r.TLS != nil && domain.SSL.MinVersion == "1.3" && r.TLS.Version < tls.VersionTLS13 {
		return http.StatusMisdirectedRequest
	}
	if domain.SSL.ClientCA == "" {
		return 0
	}
	mode := domain.SSL.ClientAuth
	if mode != "require" && mode != "request" {
		return 0
	}
	if r.TLS == nil {
		if mode == "require" {
			return http.StatusForbidden
		}
		return 0
	}
	sni, configured := s.vhosts.LookupWithStatus(r.TLS.ServerName)
	if r.TLS.ServerName == "" || !configured || sni == nil || !strings.EqualFold(sni.Host, domain.Host) {
		return http.StatusMisdirectedRequest
	}
	if mode == "require" && len(r.TLS.VerifiedChains) == 0 {
		return http.StatusForbidden
	}
	return 0
}
