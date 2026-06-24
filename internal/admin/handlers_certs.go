package admin

import (
	"net/http"
	"time"

	uwastls "github.com/uwaserver/uwas/internal/tls"
	"github.com/uwaserver/uwas/internal/webhook"
)

// --- Certificates ---

// SetTLSManager sets the TLS manager for certificate status and renewal.
func (s *Server) SetTLSManager(m *uwastls.Manager) { s.tlsMgr = m }

func (s *Server) handleCerts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.configMu.RLock()
	defer s.configMu.RUnlock()

	type certInfo struct {
		Host          string `json:"host"`
		Domain        string `json:"domain,omitempty"`
		MainHost      string `json:"main_host,omitempty"`
		CanonicalHost string `json:"canonical_host,omitempty"`
		Alias         bool   `json:"alias,omitempty"`
		SSLMode       string `json:"ssl_mode"`
		Status        string `json:"status"`
		Issuer        string `json:"issuer"`
		Expiry        string `json:"expiry,omitempty"`
		DaysLeft      int    `json:"days_left"`
	}

	certs := make([]certInfo, 0)
	for _, d := range s.config.Domains {
		mainHost := mainDomainHostname(d)
		canonicalHost := normalizeCanonicalHostPreference(d.CanonicalHost)
		for _, host := range domainHostnames(d) {
			ci := certInfo{
				Host:          host,
				Domain:        canonicalDomainHostname(d.Host),
				MainHost:      mainHost,
				CanonicalHost: canonicalHost,
				Alias:         host != mainHost,
				SSLMode:       d.SSL.Mode,
			}
			switch d.SSL.Mode {
			case "off":
				ci.Status = "none"
			case "auto":
				// Check real cert status from TLS manager.
				if s.tlsMgr != nil {
					if info := s.tlsMgr.CertStatus(host); info != nil {
						ci.Status = "active"
						ci.Issuer = info.Issuer
						ci.Expiry = info.Expiry.Format(time.RFC3339)
						ci.DaysLeft = info.DaysLeft
						if info.DaysLeft <= 0 {
							ci.Status = "expired"
						}
					} else {
						ci.Status = "pending"
						ci.Issuer = "Let's Encrypt"
					}
				} else {
					ci.Status = "pending"
					ci.Issuer = "Let's Encrypt"
				}
			case "manual":
				ci.Status = "active"
				ci.Issuer = "Manual"
				if s.tlsMgr != nil {
					if info := s.tlsMgr.CertStatus(host); info != nil {
						ci.Issuer = info.Issuer
						ci.Expiry = info.Expiry.Format(time.RFC3339)
						ci.DaysLeft = info.DaysLeft
						if info.DaysLeft <= 0 {
							ci.Status = "expired"
						}
					}
				}
			}
			certs = append(certs, ci)
		}
	}
	jsonResponse(w, certs)
}

func (s *Server) handleCertRenew(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	host := r.PathValue("host")
	if s.tlsMgr == nil {
		jsonError(w, "TLS manager not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.tlsMgr.RenewCert(r.Context(), host); err != nil {
		jsonError(w, "renewal failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventCertRenewed, map[string]any{
			"host": host,
		})
	}
	jsonResponse(w, map[string]string{"status": "renewed", "host": host})
}
