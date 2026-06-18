package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/firewall"
	"github.com/uwaserver/uwas/internal/siteuser"
)

// Test seams for firewall ops that shell out to ufw (TestMain points these at
// safe no-ops so `go test` never runs real ufw commands).
var (
	firewallGetStatus  = firewall.GetStatus
	firewallAllowPort  = firewall.AllowPort
	firewallDenyPort   = firewall.DenyPort
	firewallDeleteRule = firewall.DeleteRule
	firewallEnable     = firewall.Enable
	firewallDisable    = firewall.Disable
)

// ============ Firewall ============

func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, firewallGetStatus())
}

func (s *Server) handleFirewallAllow(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Port  string `json:"port"`
		Proto string `json:"proto"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Port == "" {
		jsonError(w, "port is required", http.StatusBadRequest)
		return
	}
	if err := firewallAllowPort(req.Port, req.Proto); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("firewall allow", "port", req.Port, "proto", req.Proto)
	jsonResponse(w, map[string]string{"status": "allowed", "port": req.Port})
}

func (s *Server) handleFirewallDeny(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Port  string `json:"port"`
		Proto string `json:"proto"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Port == "" {
		jsonError(w, "port is required", http.StatusBadRequest)
		return
	}
	if err := firewallDenyPort(req.Port, req.Proto); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("firewall deny", "port", req.Port, "proto", req.Proto)
	jsonResponse(w, map[string]string{"status": "denied", "port": req.Port})
}

func (s *Server) handleFirewallDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	numStr := r.PathValue("number")
	var num int
	fmt.Sscanf(numStr, "%d", &num)
	if num <= 0 {
		jsonError(w, "invalid rule number", http.StatusBadRequest)
		return
	}
	if err := firewallDeleteRule(num); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleFirewallEnable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := firewallEnable(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "enabled"})
}

func (s *Server) handleFirewallDisable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if err := firewallDisable(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "disabled"})
}

// ============ SSH Keys ============

func (s *Server) handleSSHKeyList(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	identity := domain
	if appName, ok := appSFTPTargetName(domain); ok {
		if !s.requireAdmin(w, r) {
			s.recordAuditR(r, "ssh.keys.list", "app: "+domain+" (forbidden)", false)
			return
		}
		identity = appSFTPIdentity(appName)
	} else {
		if !s.requireDomainAccess(w, r, domain, "ssh.keys.list") {
			return
		}
	}
	root, err := s.siteUserRoot(domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if root == "" {
		jsonError(w, "domain root not found", http.StatusNotFound)
		return
	}

	keys := siteuser.ListSSHKeysForWebDir(root, identity)
	if keys == nil {
		keys = []string{}
	}
	jsonResponse(w, keys)
}

func (s *Server) handleSSHKeyAdd(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	identity := domain
	if appName, ok := appSFTPTargetName(domain); ok {
		if !s.requireAdmin(w, r) {
			s.recordAuditR(r, "ssh.keys.add", "app: "+domain+" (forbidden)", false)
			return
		}
		identity = appSFTPIdentity(appName)
	} else {
		if !s.requireDomainAccess(w, r, domain, "ssh.keys.add") {
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.PublicKey, "ssh-") {
		jsonError(w, "invalid SSH public key (must start with ssh-)", http.StatusBadRequest)
		return
	}

	root, err := s.siteUserRoot(domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if root == "" {
		jsonError(w, "domain root not found", http.StatusNotFound)
		return
	}

	if err := siteuser.AddSSHKeyForWebDir(root, identity, req.PublicKey); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("SSH key added", "target", domain, "identity", identity)
	jsonResponse(w, map[string]string{"status": "added"})
}

func (s *Server) handleSSHKeyDelete(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	identity := domain
	if appName, ok := appSFTPTargetName(domain); ok {
		if !s.requireAdmin(w, r) {
			s.recordAuditR(r, "ssh.keys.delete", "app: "+domain+" (forbidden)", false)
			return
		}
		identity = appSFTPIdentity(appName)
	} else {
		if !s.requireDomainAccess(w, r, domain, "ssh.keys.delete") {
			return
		}
	}
	if !s.requirePin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	root, err := s.siteUserRoot(domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if root == "" {
		jsonError(w, "domain root not found", http.StatusNotFound)
		return
	}

	if err := siteuser.RemoveSSHKeyForWebDir(root, identity, req.Fingerprint); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "removed"})
}
