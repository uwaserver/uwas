package admin

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
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
	firewallDisable    = firewall.Disable

	firewallEnableWithRollback = firewall.EnableWithRollback
	firewallConfirmEnable      = firewall.ConfirmEnable
)

// firewallRollbackWindow is how long after enabling ufw the panel has to
// confirm access before the firewall auto-disables. Long enough to click the
// button, short enough that a locked-out operator is not stranded.
const firewallRollbackWindow = 60 * time.Second

// uwasFirewallPorts returns the TCP ports UWAS listens on, so enabling the
// firewall does not cut them off. SSH (22) is always included — losing it is
// the whole reason people fear `ufw enable` — and localhost-only listeners are
// skipped since the firewall governs external traffic they never receive.
func uwasFirewallPorts(g config.GlobalConfig) []string {
	seen := map[string]bool{}
	var ports []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		ports = append(ports, p)
	}
	// SSH and the web ports are the non-negotiable ones.
	add("22")
	add("80")
	add("443")
	for _, listen := range []string{g.HTTPListen, g.HTTPSListen, g.Admin.Listen, g.SFTPListen, g.MCP.Listen} {
		if listen == "" {
			continue
		}
		host, port, err := net.SplitHostPort(listen)
		if err != nil || port == "" {
			continue
		}
		// A service bound to loopback is not reachable from outside, so it
		// needs no allow rule.
		if host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost") {
			continue
		}
		add(port)
	}
	return ports
}

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
	if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
		jsonError(w, "invalid rule number", http.StatusBadRequest)
		return
	}
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
	s.configMu.RLock()
	ports := uwasFirewallPorts(s.config.Global)
	s.configMu.RUnlock()

	// Allow UWAS's own ports first, then enable with an automatic rollback: if
	// the enable drops the operator's connection they cannot confirm, and the
	// firewall reverts on its own before they are locked out for good.
	if err := firewallEnableWithRollback(firewallRollbackWindow, ports); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("firewall enabled with rollback", "allowed_ports", ports,
		"rollback_seconds", int(firewallRollbackWindow.Seconds()))
	jsonResponse(w, map[string]any{
		"status":           "enabled",
		"rollback_seconds": int(firewallRollbackWindow.Seconds()),
		"allowed_ports":    ports,
	})
}

// handleFirewallConfirm cancels the pending auto-rollback: the operator has
// confirmed they still have access, so the firewall stays on.
func (s *Server) handleFirewallConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	confirmed := firewallConfirmEnable()
	s.logger.Info("firewall enable confirmed", "was_pending", confirmed)
	jsonResponse(w, map[string]any{"status": "confirmed", "was_pending": confirmed})
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
