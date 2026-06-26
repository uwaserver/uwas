package admin

import (
	"encoding/json"
	"net/http"

	"github.com/uwaserver/uwas/internal/dnschecker"
	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/serverip"
)

// ============ DNS Checker ============

func (s *Server) handleDNSCheck(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if domain == "" {
		jsonError(w, "domain required", http.StatusBadRequest)
		return
	}
	result := dnschecker.Check(domain)
	jsonResponse(w, result)
}

// ============ DNS Records Management ============

func (s *Server) getDNSProvider() dnsmanager.Provider {
	s.configMu.RLock()
	provider := s.config.Global.ACME.DNSProvider
	creds := s.config.Global.ACME.DNSCredentials
	s.configMu.RUnlock()

	if creds == nil {
		return nil
	}

	token := creds["api_token"]
	if token == "" {
		token = creds["token"]
	}

	switch provider {
	case "cloudflare":
		if token == "" {
			return nil
		}
		return dnsmanager.NewCloudflare(token)
	case "hetzner":
		if token == "" {
			return nil
		}
		return dnsmanager.NewHetzner(token)
	case "digitalocean":
		if token == "" {
			return nil
		}
		return dnsmanager.NewDigitalOcean(token)
	case "route53":
		accessKey := creds["access_key"]
		secretKey := creds["secret_key"]
		region := creds["region"]
		if accessKey == "" || secretKey == "" {
			return nil
		}
		if region == "" {
			region = "us-east-1"
		}
		return dnsmanager.NewRoute53(accessKey, secretKey, region)
	default:
		return nil
	}
}

func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	// Per-domain authorization: the write siblings already require this; without
	// it any authenticated user could enumerate any zone's records (A/MX/TXT,
	// incl. SPF/DKIM/ACME tokens) via the shared DNS provider token.
	if !s.requireDomainAccess(w, r, domain, "dns.read") {
		return
	}
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured — set dns_provider and credentials in Settings → ACME", http.StatusNotImplemented)
		return
	}
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	records, err := cf.ListRecords(zone.ID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"zone_id": zone.ID, "zone": zone.Name, "records": records})
}

func (s *Server) handleDNSRecordCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	if !s.requireDomainAccess(w, r, domain, "dns.create") {
		return
	}
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var rec dnsmanager.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	created, err := cf.CreateRecord(zone.ID, rec)
	if err != nil {
		jsonError(w, "create record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("DNS record created", "domain", domain, "type", rec.Type, "name", rec.Name, "content", rec.Content)
	jsonResponse(w, created)
}

func (s *Server) handleDNSRecordDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !s.requirePin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	if !s.requireDomainAccess(w, r, domain, "dns.delete") {
		return
	}
	recordID := r.PathValue("id")
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	if err := cf.DeleteRecord(zone.ID, recordID); err != nil {
		jsonError(w, "delete record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("DNS record deleted", "domain", domain, "record_id", recordID)
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleDNSRecordUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	if !s.requireDomainAccess(w, r, domain, "dns.update") {
		return
	}
	recordID := r.PathValue("id")
	prov := s.getDNSProvider()
	if prov == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var rec dnsmanager.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := prov.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	updated, err := prov.UpdateRecord(zone.ID, recordID, rec)
	if err != nil {
		jsonError(w, "update record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("DNS record updated", "domain", domain, "record_id", recordID, "type", rec.Type, "content", rec.Content)
	jsonResponse(w, updated)
}

func (s *Server) handleDNSSync(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	if !s.requireDomainAccess(w, r, domain, "dns.sync") {
		return
	}
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}

	// Get server's public IP
	ip := serverip.PublicIP()
	if ip == "" {
		jsonError(w, "could not detect server IP", http.StatusInternalServerError)
		return
	}

	// Find zone and sync A record
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	records, err := cf.ListRecords(zone.ID)
	if err != nil {
		jsonError(w, "list records: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Find existing A record or create new one
	found := false
	for _, rec := range records {
		if rec.Type == "A" && (rec.Name == domain || rec.Name == "@") {
			if rec.Content != ip {
				rec.Content = ip
				if _, err := cf.UpdateRecord(zone.ID, rec.ID, rec); err != nil {
					jsonError(w, "update A record: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
			found = true
			break
		}
	}
	if !found {
		if _, err := cf.CreateRecord(zone.ID, dnsmanager.Record{Type: "A", Name: domain, Content: ip, TTL: 1}); err != nil {
			jsonError(w, "create A record: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.logger.Info("DNS synced", "domain", domain, "ip", ip)
	jsonResponse(w, map[string]string{"status": "synced", "domain": domain, "ip": ip})
}
