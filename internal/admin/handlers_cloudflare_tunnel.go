package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	cfintegration "github.com/uwaserver/uwas/internal/cloudflare"
)

type tunnelView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	LocalTarget string    `json:"local_target"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Running     bool      `json:"running"`
	PID         int       `json:"pid,omitempty"`
	Uptime      string    `json:"uptime,omitempty"`
}

func (s *Server) tunnelToView(t cloudflareTunnel) tunnelView {
	view := tunnelView{
		ID:          t.ID,
		Name:        t.Name,
		Hostname:    t.Hostname,
		LocalTarget: t.LocalTarget,
		CreatedAt:   t.CreatedAt,
	}
	if s.cfRunner != nil {
		st := s.cfRunner.StatusOf(t.ID)
		view.Running = st.Running
		view.PID = st.PID
		view.Uptime = st.Uptime
	}
	return view
}

func (s *Server) handleCloudflareTunnels(w http.ResponseWriter, r *http.Request) {
	// Tunnel inventory includes local targets, process IDs, and account-wide
	// routing topology; keep it admin-only unless a scoped view is added later.
	if !s.requireAdmin(w, r) {
		return
	}
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	if cfg == nil || !cfg.Connected {
		jsonResponse(w, []tunnelView{})
		return
	}
	views := make([]tunnelView, 0, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		views = append(views, s.tunnelToView(t))
	}
	jsonResponse(w, views)
}

// validateLocalTarget enforces a small whitelist of cloudflared service URLs.
// We accept http(s)://host:port, tcp://host:port, ssh://host:port, and the
// literal "http_status:NNN" placeholder.
func validateLocalTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("local_target is required (e.g. http://localhost:8080)")
	}
	if strings.HasPrefix(target, "http_status:") {
		return nil
	}
	for _, scheme := range []string{"http://", "https://", "tcp://", "ssh://", "rdp://", "unix:"} {
		if strings.HasPrefix(target, scheme) {
			return nil
		}
	}
	return fmt.Errorf("local_target must start with one of: http://, https://, tcp://, ssh://, rdp://, unix:, http_status")
}

func (s *Server) handleCloudflareTunnelCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()
	if cfg == nil || !cfg.Connected {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Name        string `json:"name"`
		Hostname    string `json:"hostname"`
		LocalTarget string `json:"local_target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Hostname = strings.TrimSpace(strings.ToLower(req.Hostname))
	req.LocalTarget = strings.TrimSpace(req.LocalTarget)

	if req.Name == "" || req.Hostname == "" {
		jsonError(w, "name and hostname are required", http.StatusBadRequest)
		return
	}
	if !isValidHostname(req.Hostname) {
		jsonError(w, "invalid hostname", http.StatusBadRequest)
		return
	}
	if err := validateLocalTarget(req.LocalTarget); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Reject duplicate tunnel names locally — Cloudflare also enforces uniqueness.
	for _, t := range cfg.Tunnels {
		if strings.EqualFold(t.Name, req.Name) {
			jsonError(w, "a tunnel named "+req.Name+" already exists", http.StatusConflict)
			return
		}
		if strings.EqualFold(t.Hostname, req.Hostname) {
			jsonError(w, "hostname "+req.Hostname+" is already attached to a tunnel", http.StatusConflict)
			return
		}
	}

	cli := cfintegration.New(cfg.Token, cfg.AccountID)

	// 1. Resolve the zone that owns the hostname.
	zone, err := cli.FindZoneByHostname(req.Hostname)
	if err != nil {
		jsonError(w, "zone lookup: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 2. Create the tunnel.
	cft, err := cli.CreateTunnel(req.Name)
	if err != nil {
		jsonError(w, "create tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 3. Attach ingress rules (hostname → local target, fallback 404).
	rules := []cfintegration.IngressRule{
		{Hostname: req.Hostname, Service: req.LocalTarget},
		{Service: "http_status:404"},
	}
	if err := cli.PutTunnelConfig(cft.ID, rules); err != nil {
		_ = cli.DeleteTunnel(cft.ID)
		jsonError(w, "put tunnel config: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 4. Create the proxied CNAME at hostname → <tunnel>.cfargotunnel.com.
	recordID, err := cli.CreateTunnelCNAME(zone.ID, req.Hostname, cft.ID)
	if err != nil {
		_ = cli.DeleteTunnel(cft.ID)
		jsonError(w, "create DNS CNAME: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 5. Fetch the connector token (needed to run cloudflared).
	token, err := cli.GetTunnelToken(cft.ID)
	if err != nil {
		_ = cli.DeleteDNSRecord(zone.ID, recordID)
		_ = cli.DeleteTunnel(cft.ID)
		jsonError(w, "get connector token: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 6. Persist.
	tunnel := cloudflareTunnel{
		ID:             cft.ID,
		Name:           req.Name,
		Hostname:       req.Hostname,
		LocalTarget:    req.LocalTarget,
		ConnectorToken: token,
		ZoneID:         zone.ID,
		DNSRecordID:    recordID,
		CreatedAt:      time.Now(),
	}
	cloudflareMu.Lock()
	cloudflareConfig.Tunnels = append(cloudflareConfig.Tunnels, tunnel)
	cloudflareConfig.UpdatedAt = time.Now()
	if err := s.saveCloudflareStateLocked(); err != nil {
		s.logger.Error("cloudflare state save failed", "error", err.Error())
	}
	cloudflareMu.Unlock()

	s.recordAuditR(r, "cloudflare.tunnel.create", req.Name+" → "+req.Hostname, true)
	jsonResponse(w, s.tunnelToView(tunnel))
}

func (s *Server) findTunnel(id string) (cloudflareTunnel, bool) {
	cloudflareMu.RLock()
	defer cloudflareMu.RUnlock()
	if cloudflareConfig == nil {
		return cloudflareTunnel{}, false
	}
	for _, t := range cloudflareConfig.Tunnels {
		if t.ID == id {
			return t, true
		}
	}
	return cloudflareTunnel{}, false
}

func (s *Server) handleCloudflareTunnelDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()
	if cfg == nil {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}
	t, ok := s.findTunnel(id)
	if !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}

	// 1. Stop the local cloudflared process so CF can delete the tunnel.
	if s.cfRunner != nil {
		if err := s.cfRunner.Stop(id); err != nil {
			s.logger.Warn("cloudflared stop on delete failed", "tunnel_id", id, "error", err.Error())
		}
	}

	cli := cfintegration.New(cfg.Token, cfg.AccountID)

	// 2. Delete the DNS record (best-effort).
	if t.ZoneID != "" && t.DNSRecordID != "" {
		if err := cli.DeleteDNSRecord(t.ZoneID, t.DNSRecordID); err != nil {
			s.logger.Warn("DNS record delete failed", "zone", t.ZoneID, "record", t.DNSRecordID, "error", err.Error())
		}
	}

	// 3. Delete the tunnel itself.
	if err := cli.DeleteTunnel(id); err != nil {
		jsonError(w, "delete tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 4. Remove from state.
	cloudflareMu.Lock()
	newTunnels := make([]cloudflareTunnel, 0, len(cloudflareConfig.Tunnels))
	for _, x := range cloudflareConfig.Tunnels {
		if x.ID != id {
			newTunnels = append(newTunnels, x)
		}
	}
	cloudflareConfig.Tunnels = newTunnels
	cloudflareConfig.UpdatedAt = time.Now()
	if err := s.saveCloudflareStateLocked(); err != nil {
		s.logger.Error("cloudflare state save failed", "error", err.Error())
	}
	cloudflareMu.Unlock()

	if s.cfRunner != nil {
		s.cfRunner.Forget(id)
	}

	s.recordAuditR(r, "cloudflare.tunnel.delete", "id: "+id, true)
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleCloudflareTunnelStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	t, ok := s.findTunnel(id)
	if !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}

	// Re-fetch token if missing (e.g. legacy state).
	token := t.ConnectorToken
	if token == "" {
		cloudflareMu.RLock()
		cfg := cloudflareConfig
		cloudflareMu.RUnlock()
		if cfg == nil {
			jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
			return
		}
		cli := cfintegration.New(cfg.Token, cfg.AccountID)
		fresh, err := cli.GetTunnelToken(id)
		if err != nil {
			jsonError(w, "fetch connector token: "+err.Error(), http.StatusBadGateway)
			return
		}
		token = fresh
		cloudflareMu.Lock()
		for i := range cloudflareConfig.Tunnels {
			if cloudflareConfig.Tunnels[i].ID == id {
				cloudflareConfig.Tunnels[i].ConnectorToken = token
				break
			}
		}
		_ = s.saveCloudflareStateLocked()
		cloudflareMu.Unlock()
	}

	if s.cfRunner == nil {
		jsonError(w, "tunnel runner not initialized", http.StatusInternalServerError)
		return
	}
	if err := s.cfRunner.Start(id, token); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditR(r, "cloudflare.tunnel.start", "id: "+id, true)
	jsonResponse(w, map[string]string{"status": "started"})
}

func (s *Server) handleCloudflareTunnelStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	if _, ok := s.findTunnel(id); !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	if s.cfRunner == nil {
		jsonError(w, "tunnel runner not initialized", http.StatusInternalServerError)
		return
	}
	if err := s.cfRunner.Stop(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "cloudflare.tunnel.stop", "id: "+id, true)
	jsonResponse(w, map[string]string{"status": "stopped"})
}

// handleCloudflareTunnelLogs returns the last ~64 lines from the tunnel's
// cloudflared process. Useful for debugging connection issues from the UI.
func (s *Server) handleCloudflareTunnelLogs(w http.ResponseWriter, r *http.Request) {
	// cloudflared logs can disclose internal hostnames, local service targets,
	// tunnel IDs, and operational diagnostics for the whole account.
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if _, ok := s.findTunnel(id); !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	if s.cfRunner == nil {
		jsonResponse(w, map[string]string{"logs": ""})
		return
	}
	jsonResponse(w, map[string]string{"logs": s.cfRunner.Tail(id)})
}

// handleCloudflaredInstall installs the cloudflared binary via the system
// package manager. Linux only.
func (s *Server) handleCloudflaredInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	info, err := cfintegration.InstallCloudflared()
	if err != nil {
		s.recordAuditR(r, "cloudflare.cloudflared.install", "failed: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "cloudflare.cloudflared.install", "version: "+info.Version, true)
	jsonResponse(w, info)
}
