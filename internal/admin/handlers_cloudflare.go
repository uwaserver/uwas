package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cfintegration "github.com/uwaserver/uwas/internal/cloudflare"
	"github.com/uwaserver/uwas/internal/config"
)

// --- Cloudflare Integration ---

type cloudflareTunnel struct {
	ID             string    `json:"id"` // real Cloudflare tunnel UUID
	Name           string    `json:"name"`
	Hostname       string    `json:"hostname"`        // public, e.g. app.example.com
	LocalTarget    string    `json:"local_target"`    // http://localhost:8080 or tcp://localhost:22
	ConnectorToken string    `json:"connector_token"` // from Cloudflare API; never returned to client
	ZoneID         string    `json:"zone_id"`
	DNSRecordID    string    `json:"dns_record_id"`
	CreatedAt      time.Time `json:"created_at,omitempty"`

	// Legacy v0.1.6 stub field kept only so old state files unmarshal without
	// dropping the value; migrated to Hostname once on load when SchemaVersion<2.
	// Slated for removal after v0.5 (refactor.md A21).
	Domain string `json:"domain,omitempty"`
}

// cloudflareStateSchemaCurrent is the current schema version persisted on
// disk. v0.1.6 wrote no version (treated as 1); v0.2.0+ writes 2.
const cloudflareStateSchemaCurrent = 2

type cloudflareState struct {
	SchemaVersion int                `json:"schema_version,omitempty"`
	Token         string             `json:"token"`
	AccountID     string             `json:"account_id"`
	Email         string             `json:"email"`
	Tunnels       []cloudflareTunnel `json:"tunnels"`
	Connected     bool               `json:"connected"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

var (
	cloudflareMu     sync.RWMutex
	cloudflareConfig *cloudflareState
)

func (s *Server) handleCloudflareStatus(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	cfd := cfintegration.DetectCloudflared()

	if cfg == nil {
		jsonResponse(w, map[string]any{
			"connected":             false,
			"cloudflared_installed": cfd.Installed,
			"cloudflared_version":   cfd.Version,
		})
		return
	}

	jsonResponse(w, map[string]any{
		"connected":             cfg.Connected,
		"email":                 cfg.Email,
		"account_id":            cfg.AccountID,
		"token_mask":            maskCloudflareToken(cfg.Token),
		"updated_at":            cfg.UpdatedAt,
		"tunnel_count":          len(cfg.Tunnels),
		"cloudflared_installed": cfd.Installed,
		"cloudflared_version":   cfd.Version,
	})
}

func (s *Server) handleCloudflareIPs(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	ranges := append([]string(nil), s.config.Global.Cloudflare.IPRanges...)
	lastSynced := s.config.Global.Cloudflare.LastSynced
	s.configMu.RUnlock()
	jsonResponse(w, map[string]any{
		"ip_ranges":   ranges,
		"last_synced": lastSynced,
		"count":       len(ranges),
	})
}

func (s *Server) handleCloudflareIPsUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		IPRanges []string `json:"ip_ranges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.recordAuditR(r, "cloudflare.ips.update", "invalid JSON body", false)
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	ranges, err := cfintegration.NormalizeCIDRs(req.IPRanges)
	if err != nil {
		s.recordAuditR(r, "cloudflare.ips.update", err.Error(), false)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	s.config.Global.Cloudflare.IPRanges = ranges
	s.configMu.Unlock()
	s.persistConfig()
	s.recordAuditR(r, "cloudflare.ips.update", fmt.Sprintf("ranges: %d", len(ranges)), true)
	jsonResponse(w, map[string]any{
		"status":    "updated",
		"ip_ranges": ranges,
		"count":     len(ranges),
	})
}

func (s *Server) handleCloudflareIPsSync(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ranges, err := cfintegration.FetchIPRanges(r.Context())
	if err != nil {
		s.recordAuditR(r, "cloudflare.ips.sync", err.Error(), false)
		jsonError(w, "sync failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	s.configMu.Lock()
	s.config.Global.Cloudflare.IPRanges = ranges
	s.config.Global.Cloudflare.LastSynced = now
	s.configMu.Unlock()
	s.persistConfig()
	s.recordAuditR(r, "cloudflare.ips.sync", fmt.Sprintf("ranges: %d", len(ranges)), true)
	jsonResponse(w, map[string]any{
		"status":      "synced",
		"ip_ranges":   ranges,
		"last_synced": now,
		"count":       len(ranges),
	})
}

func (s *Server) handleCloudflareConnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Token     string `json:"token"`
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Token == "" || req.AccountID == "" {
		jsonError(w, "token and account_id are required", http.StatusBadRequest)
		return
	}

	// Validate token by fetching zones
	email, err := s.validateCloudflareToken(req.Token, req.AccountID)
	if err != nil {
		jsonError(w, "invalid token: "+err.Error(), http.StatusBadRequest)
		return
	}

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token:     req.Token,
		AccountID: req.AccountID,
		Email:     email,
		Tunnels:   []cloudflareTunnel{},
		Connected: true,
		UpdatedAt: time.Now(),
	}
	saveErr := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if saveErr != nil {
		s.logger.Error("cloudflare state save failed", "error", saveErr.Error())
	}

	s.recordAuditR(r, "cloudflare.connect", "account: "+req.AccountID, true)
	jsonResponse(w, map[string]string{"status": "connected"})
}

func (s *Server) validateCloudflareToken(token, accountID string) (string, error) {
	// Call Cloudflare API to validate token and get user info
	req, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Result  struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			ExpiresOn string `json:"expires_on"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if !result.Success {
		if len(result.Errors) > 0 {
			return "", fmt.Errorf("%s", result.Errors[0].Message)
		}
		return "", fmt.Errorf("token validation failed")
	}

	// Get account info
	req2, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/accounts/"+accountID, nil)
	if err != nil {
		return "", err
	}
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	var accResult struct {
		Success bool `json:"success"`
		Result  struct {
			Name string `json:"name"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&accResult); err != nil {
		return "", err
	}
	if !accResult.Success {
		if len(accResult.Errors) > 0 {
			return "", fmt.Errorf("%s", accResult.Errors[0].Message)
		}
		return "", fmt.Errorf("account validation failed")
	}

	return accResult.Result.Name, nil
}

func (s *Server) handleCloudflareDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	cloudflareMu.Lock()
	oldCfg := cloudflareConfig
	cloudflareConfig = nil
	saveErr := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if saveErr != nil {
		s.logger.Error("cloudflare state save failed", "error", saveErr.Error())
	}

	if oldCfg != nil {
		s.recordAuditR(r, "cloudflare.disconnect", "account: "+oldCfg.AccountID, true)
	}

	jsonResponse(w, map[string]string{"status": "disconnected"})
}

// tunnelView is the JSON shape returned to the dashboard. Connector token is
// never exposed to the client.
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

func (s *Server) handleCloudflareCachePurge(w http.ResponseWriter, r *http.Request) {
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
		URL        string `json:"url"`
		Everything bool   `json:"everything"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Call Cloudflare API to purge cache
	err := s.purgeCloudflareCache(cfg.Token, req.URL, req.Everything)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditR(r, "cloudflare.cache.purge", "url: "+req.URL+", everything: "+fmt.Sprintf("%v", req.Everything), true)
	jsonResponse(w, map[string]string{"status": "purged"})
}

func (s *Server) purgeCloudflareCache(token, url string, everything bool) error {
	// Get zones first
	zones, err := s.fetchCloudflareZones(token)
	if err != nil {
		return err
	}

	for _, zone := range zones {
		var payload []byte
		if everything {
			payload = []byte(`{"purge_everything":true}`)
		} else if url != "" {
			payload = []byte(`{"files":["` + url + `"]}`)
		} else {
			continue
		}

		req, err := http.NewRequest("POST", "https://api.cloudflare.com/client/v4/zones/"+zone.ID+"/purge_cache", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Read and discard response body to ensure connection reuse
		io.Copy(io.Discard, resp.Body)
	}

	return nil
}

func (s *Server) handleCloudflareZones(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	if cfg == nil || !cfg.Connected {
		jsonResponse(w, []any{})
		return
	}

	zones, err := s.fetchCloudflareZones(cfg.Token)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, zones)
}

// fetchCloudflareZones iterates all pages of /zones (50 per page) so accounts
// with hundreds of zones get the full list. Hard-capped at 50 pages (2500
// zones) to bound memory and avoid runaway loops on a misbehaving API.
func (s *Server) fetchCloudflareZones(token string) ([]cloudflareZone, error) {
	const perPage = 50
	const maxPages = 50

	all := make([]cloudflareZone, 0, perPage)
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?per_page=%d&page=%d", perPage, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		var result struct {
			Success bool `json:"success"`
			Result  []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
				Plan   struct {
					Name string `json:"name"`
				} `json:"plan"`
			} `json:"result"`
			ResultInfo struct {
				Page       int `json:"page"`
				PerPage    int `json:"per_page"`
				Count      int `json:"count"`
				TotalCount int `json:"total_count"`
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		if !result.Success {
			if len(result.Errors) > 0 {
				return nil, fmt.Errorf("%s", result.Errors[0].Message)
			}
			return nil, fmt.Errorf("failed to fetch zones (page %d)", page)
		}

		for _, z := range result.Result {
			all = append(all, cloudflareZone{
				ID:     z.ID,
				Name:   z.Name,
				Status: z.Status,
				Plan:   z.Plan.Name,
			})
		}

		// Stop if we've fetched everything.
		if result.ResultInfo.TotalPages == 0 || page >= result.ResultInfo.TotalPages {
			break
		}
		// Defensive: also stop if this page returned fewer than per_page items.
		if len(result.Result) < perPage {
			break
		}
	}
	return all, nil
}

type cloudflareZone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Plan   string `json:"plan,omitempty"`
}

func (s *Server) fetchCloudflareDNSRecords(token, zoneID string) ([]cloudflareDNSRecord, error) {
	req, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/zones/"+zoneID+"/dns_records", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Result  []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Name     string `json:"name"`
			Content  string `json:"content"`
			TTL      int    `json:"ttl"`
			Proxied  bool   `json:"proxied"`
			Priority int    `json:"priority"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.Success {
		if len(result.Errors) > 0 {
			return nil, fmt.Errorf("%s", result.Errors[0].Message)
		}
		return nil, fmt.Errorf("failed to fetch DNS records")
	}

	records := make([]cloudflareDNSRecord, len(result.Result))
	for i, r := range result.Result {
		records[i] = cloudflareDNSRecord{
			ID:       r.ID,
			Type:     r.Type,
			Name:     r.Name,
			Content:  r.Content,
			TTL:      r.TTL,
			Proxied:  r.Proxied,
			Priority: r.Priority,
		}
	}
	return records, nil
}

type cloudflareDNSRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority"`
}

// handleCloudflareZoneImport pulls A/AAAA/CNAME hostnames from a Cloudflare
// zone and creates UWAS domain entries for any hostname not already configured.
// Body: { "default_type": "static"|"php"|"proxy", "default_root": "/var/www/{host}/public_html" }
func (s *Server) handleCloudflareZoneImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	zoneID := r.PathValue("id")
	if zoneID == "" {
		jsonError(w, "zone id required", http.StatusBadRequest)
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
		DefaultType string   `json:"default_type"`
		DefaultRoot string   `json:"default_root"`
		DryRun      bool     `json:"dry_run"`   // preview only — don't persist
		Hostnames   []string `json:"hostnames"` // optional whitelist; if set, only these are imported
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.DefaultType == "" {
		req.DefaultType = "static"
	}
	switch req.DefaultType {
	case "static", "php", "proxy", "redirect":
	default:
		jsonError(w, "default_type must be one of: static, php, proxy, redirect", http.StatusBadRequest)
		return
	}

	// Build a hostname whitelist (lowercased) if the caller supplied one.
	var whitelist map[string]bool
	if len(req.Hostnames) > 0 {
		whitelist = make(map[string]bool, len(req.Hostnames))
		for _, h := range req.Hostnames {
			whitelist[strings.ToLower(strings.TrimSuffix(h, "."))] = true
		}
	}

	records, err := s.fetchCloudflareDNSRecords(cfg.Token, zoneID)
	if err != nil {
		jsonError(w, "fetch records failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Collect unique hostnames from A/AAAA/CNAME records.
	seen := map[string]bool{}
	hostnames := make([]string, 0, len(records))
	for _, rec := range records {
		switch rec.Type {
		case "A", "AAAA", "CNAME":
		default:
			continue
		}
		host := strings.TrimSuffix(strings.ToLower(rec.Name), ".")
		if host == "" || !isValidHostname(host) {
			continue
		}
		// Skip Cloudflare tunnel infrastructure hostnames.
		if strings.HasSuffix(host, ".cfargotunnel.com") {
			continue
		}
		// If caller supplied a whitelist, only consider those hostnames.
		if whitelist != nil && !whitelist[host] {
			continue
		}
		if !seen[host] {
			seen[host] = true
			hostnames = append(hostnames, host)
		}
	}

	added := []string{}
	skipped := []string{}

	s.configMu.Lock()
	existing := map[string]bool{}
	for _, d := range s.config.Domains {
		existing[strings.ToLower(d.Host)] = true
	}

	webRoot := s.config.Global.WebRoot
	if webRoot == "" {
		webRoot = "/var/www"
	}

	// Dry-run: just figure out what would happen without touching state.
	if req.DryRun {
		for _, host := range hostnames {
			if existing[host] {
				skipped = append(skipped, host)
			} else {
				added = append(added, host)
			}
		}
		s.configMu.Unlock()
		jsonResponse(w, map[string]any{
			"added":   added,
			"skipped": skipped,
			"total":   len(hostnames),
			"dry_run": true,
		})
		return
	}

	for _, host := range hostnames {
		if existing[host] {
			skipped = append(skipped, host)
			continue
		}
		root := req.DefaultRoot
		if root == "" {
			root = filepath.Join(webRoot, host, "public_html")
		} else {
			root = strings.ReplaceAll(root, "{host}", host)
		}
		d := config.Domain{
			Host: host,
			Type: req.DefaultType,
			Root: root,
			SSL:  config.SSLConfig{Mode: "auto"},
		}
		if d.Type == "php" {
			d.PHP.IndexFiles = []string{"index.php", "index.html"}
			d.Htaccess = config.HtaccessConfig{Mode: "import"}
			d.Security.WAF.Enabled = true
			d.Security.BlockedPaths = []string{".git", ".env", "wp-config.php"}
		}
		if d.Type != "redirect" {
			d.Cache.Enabled = true
			d.Cache.TTL = 3600
		}
		// Best-effort web root creation.
		if root != "" {
			if err := os.MkdirAll(root, 0755); err != nil {
				s.logger.Warn("import: web root create failed", "domain", host, "error", err)
			}
		}
		s.config.Domains = append(s.config.Domains, d)
		existing[host] = true
		added = append(added, host)
	}
	s.configMu.Unlock()

	if len(added) > 0 {
		s.notifyDomainChange()
	}

	s.recordAuditR(r, "cloudflare.zones.import", fmt.Sprintf("zone: %s, added: %d, skipped: %d", zoneID, len(added), len(skipped)), true)

	jsonResponse(w, map[string]any{
		"added":   added,
		"skipped": skipped,
		"total":   len(hostnames),
	})
}

// maskCloudflareToken returns the last 4 chars of the token prefixed with stars.
func maskCloudflareToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}
