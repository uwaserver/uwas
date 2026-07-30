// Package cloudflare provides admin API handlers for Cloudflare integration
// (account connection, tunnel management, zone import, cache purge).
// Extracted from the admin package following the same pattern as
// internal/admin/database (refactor.md A17).
package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/respond"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	// CF API operations (tests mock cfHTTPClient in admin package)
	ValidateToken(token, accountID string) (string, error)
	FetchZones(token string) ([]Zone, error)
	FetchDNSRecords(token, zoneID string) ([]DNSRecord, error)
	PurgeCache(token, url string, everything bool) error
	NormalizeCIDRs(ranges []string) ([]string, error)
	// Logging
	LogInfo(msg string, args ...any)
	LogWarn(msg string, args ...any)
	LogError(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Config access
	CloudflareIPRanges() ([]string, string) // returns (ranges, lastSynced)
	SetCloudflareIPRanges(ranges []string, lastSynced string)
	PersistConfig()
	NotifyDomainChange()
	// Cloudflare state management
	LoadCloudflareState() *State
	SaveCloudflareState(st *State) error
	// Tunnel CRUD via CF API
	CreateTunnelAPI(token, accountID, name, hostname, localTarget string) (tunnel Tunnel, err error)
	DeleteTunnelAPI(token, accountID, tunnelID string) error
	TunnelStatusOf(id string) (running bool, pid int, uptime string)
	TunnelStart(id, token string) error
	TunnelStop(id string) error
	TunnelForget(id string)
	TunnelTail(id string) string
	// Config mutation for zone import
	AddDomain(d config.Domain)
	ExistingDomains() map[string]bool
	WebRoot() string
	// IP sync (delegates to cloudflare integration package)
	FetchIPRanges(r *http.Request) ([]string, error)
}

// State mirrors the admin package's cloudflareState (kept here to avoid
// importing admin). The admin adapter translates between the two.
type State struct {
	SchemaVersion int      `json:"schema_version,omitempty"`
	Token         string   `json:"token"`
	AccountID     string   `json:"account_id"`
	Email         string   `json:"email"`
	Tunnels       []Tunnel `json:"tunnels"`
	Connected     bool     `json:"connected"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Tunnel mirrors the admin package's cloudflareTunnel.
type Tunnel struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Hostname       string    `json:"hostname"`
	LocalTarget    string    `json:"local_target"`
	ConnectorToken string    `json:"connector_token"`
	ZoneID         string    `json:"zone_id"`
	DNSRecordID    string    `json:"dns_record_id"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	Domain         string    `json:"domain,omitempty"`
}

// TunnelView is the sanitized tunnel response.
type TunnelView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	LocalTarget string    `json:"local_target"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Running     bool      `json:"running"`
	PID         int       `json:"pid,omitempty"`
	Uptime      string    `json:"uptime,omitempty"`
}

// Zone represents a Cloudflare zone.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Plan   string `json:"plan,omitempty"`
}

// DNSRecord represents a Cloudflare DNS record.
type DNSRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority"`
}

// cfHTTPClient bounds outbound Cloudflare API calls.
var cfHTTPClient = &http.Client{Timeout: 30 * time.Second}

// Handler holds Cloudflare admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a Cloudflare Handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// ── Helpers ──

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	respond.Error(w, code, msg)
}

// maskToken returns the last 4 chars of the token prefixed with stars.
func maskToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}

func tunnelToView(t Tunnel, deps Deps) TunnelView {
	view := TunnelView{
		ID:          t.ID,
		Name:        t.Name,
		Hostname:    t.Hostname,
		LocalTarget: t.LocalTarget,
		CreatedAt:   t.CreatedAt,
	}
	running, pid, uptime := deps.TunnelStatusOf(t.ID)
	view.Running = running
	view.PID = pid
	view.Uptime = uptime
	return view
}

// ── Handlers ──

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil {
		jsonResponse(w, map[string]any{"connected": false})
		return
	}
	jsonResponse(w, map[string]any{
		"connected":  st.Connected,
		"email":      st.Email,
		"account_id": st.AccountID,
		"token_mask": maskToken(st.Token),
		"updated_at": st.UpdatedAt,
		"tunnel_count": len(st.Tunnels),
	})
}

func (h *Handler) IPs(w http.ResponseWriter, r *http.Request) {
	ranges, lastSynced := h.deps.CloudflareIPRanges()
	jsonResponse(w, map[string]any{
		"ip_ranges":   ranges,
		"last_synced": lastSynced,
		"count":       len(ranges),
	})
}

func (h *Handler) IPsUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		IPRanges []string `json:"ip_ranges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.deps.RecordAudit(r, "cloudflare.ips.update", "invalid JSON body", false)
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	ranges, err := h.deps.NormalizeCIDRs(req.IPRanges)
	if err != nil {
		h.deps.RecordAudit(r, "cloudflare.ips.update", err.Error(), false)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.deps.SetCloudflareIPRanges(ranges, "")
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "cloudflare.ips.update", fmt.Sprintf("ranges: %d", len(ranges)), true)
	jsonResponse(w, map[string]any{
		"status": "updated", "ip_ranges": ranges, "count": len(ranges),
	})
}

func (h *Handler) IPsSync(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	// Fetch from Cloudflare API — delegate to adapter
	ranges, err := h.deps.FetchIPRanges(r)
	if err != nil {
		h.deps.RecordAudit(r, "cloudflare.ips.sync", err.Error(), false)
		jsonError(w, "sync failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	h.deps.SetCloudflareIPRanges(ranges, now)
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "cloudflare.ips.sync", fmt.Sprintf("ranges: %d", len(ranges)), true)
	jsonResponse(w, map[string]any{
		"status": "synced", "ip_ranges": ranges, "last_synced": now, "count": len(ranges),
	})
}

func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
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
	email, err := h.deps.ValidateToken(req.Token, req.AccountID)
	if err != nil {
		jsonError(w, "invalid token: "+err.Error(), http.StatusBadRequest)
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil {
		st = &State{}
	}
	st.Token = req.Token
	st.AccountID = req.AccountID
	st.Email = email
	st.Tunnels = []Tunnel{}
	st.Connected = true
	st.UpdatedAt = time.Now()
	if err := h.deps.SaveCloudflareState(st); err != nil {
		h.deps.LogError("cloudflare state save failed", "error", err)
	}
	h.deps.RecordAudit(r, "cloudflare.connect", "account: "+req.AccountID, true)
	jsonResponse(w, map[string]string{"status": "connected"})
}

func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := h.deps.LoadCloudflareState()
	oldAccountID := ""
	if st != nil {
		oldAccountID = st.AccountID
		st.Connected = false
		st.Token = ""
		st.AccountID = ""
		st.Email = ""
		st.Tunnels = nil
	}
	if err := h.deps.SaveCloudflareState(st); err != nil {
		h.deps.LogError("cloudflare state save failed", "error", err)
	}
	if oldAccountID != "" {
		h.deps.RecordAudit(r, "cloudflare.disconnect", "account: "+oldAccountID, true)
	}
	jsonResponse(w, map[string]string{"status": "disconnected"})
}

func (h *Handler) CachePurge(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil || !st.Connected {
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
	if err := h.deps.PurgeCache(st.Token, req.URL, req.Everything); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "cloudflare.cache.purge", "url: "+req.URL+", everything: "+fmt.Sprintf("%v", req.Everything), true)
	jsonResponse(w, map[string]string{"status": "purged"})
}

func (h *Handler) Tunnels(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil || !st.Connected {
		jsonResponse(w, []TunnelView{})
		return
	}
	views := make([]TunnelView, 0, len(st.Tunnels))
	for _, t := range st.Tunnels {
		views = append(views, tunnelToView(t, h.deps))
	}
	jsonResponse(w, views)
}

func (h *Handler) TunnelCreate(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil || !st.Connected {
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
	for _, t := range st.Tunnels {
		if strings.EqualFold(t.Name, req.Name) {
			jsonError(w, "a tunnel named "+req.Name+" already exists", http.StatusConflict)
			return
		}
		if strings.EqualFold(t.Hostname, req.Hostname) {
			jsonError(w, "hostname "+req.Hostname+" is already attached to a tunnel", http.StatusConflict)
			return
		}
	}
	// Create tunnel via CF API (adapter handles the actual API calls).
	tunnel, err := h.deps.CreateTunnelAPI(st.Token, st.AccountID, req.Name, req.Hostname, req.LocalTarget)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}
	tunnel.CreatedAt = time.Now()
	st.Tunnels = append(st.Tunnels, tunnel)
	st.UpdatedAt = time.Now()
	if err := h.deps.SaveCloudflareState(st); err != nil {
		h.deps.LogError("cloudflare state save failed", "error", err)
	}
	h.deps.RecordAudit(r, "cloudflare.tunnel.create", req.Name+" → "+req.Hostname, true)
	jsonResponse(w, tunnelToView(tunnel, h.deps))
}

func (h *Handler) TunnelDelete(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}
	found := false
	var newTunnels []Tunnel
	for _, t := range st.Tunnels {
		if t.ID == id {
			found = true
		} else {
			newTunnels = append(newTunnels, t)
		}
	}
	if !found {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	// Delete via CF API
	if err := h.deps.DeleteTunnelAPI(st.Token, st.AccountID, id); err != nil {
		h.deps.LogWarn("CF tunnel delete failed", "id", id, "error", err)
	}
	st.Tunnels = newTunnels
	st.UpdatedAt = time.Now()
	h.deps.TunnelStop(id)
	h.deps.TunnelForget(id)
	h.deps.SaveCloudflareState(st)
	h.deps.RecordAudit(r, "cloudflare.tunnel.delete", "id: "+id, true)
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (h *Handler) TunnelStart(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	st := h.deps.LoadCloudflareState()
	// Check tunnel existence first (404) before connected state.
	var found bool
	var connectorToken string
	if st != nil {
		for _, t := range st.Tunnels {
			if t.ID == id {
				found = true
				connectorToken = t.ConnectorToken
				break
			}
		}
	}
	if !found {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	token := connectorToken
	if token == "" {
		jsonError(w, "no connector token for tunnel", http.StatusBadRequest)
		return
	}
	if err := h.deps.TunnelStart(id, token); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "cloudflare.tunnel.start", "id: "+id, true)
	jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handler) TunnelStop(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	st := h.deps.LoadCloudflareState()
	// Check tunnel existence first (404).
	found := false
	if st != nil {
		for _, t := range st.Tunnels {
			if t.ID == id {
				found = true
				break
			}
		}
	}
	if !found {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	if err := h.deps.TunnelStop(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "cloudflare.tunnel.stop", "id: "+id, true)
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (h *Handler) TunnelLogs(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	st := h.deps.LoadCloudflareState()
	found := false
	if st != nil {
		for _, t := range st.Tunnels {
			if t.ID == id {
				found = true
				break
			}
		}
	}
	if !found {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"logs": h.deps.TunnelTail(id)})
}

func (h *Handler) CloudflaredInstall(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	// Defer to adapter for actual install
	jsonError(w, "cloudflared install requires system integration", http.StatusNotImplemented)
}

func (h *Handler) Zones(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil || !st.Connected {
		jsonResponse(w, []any{})
		return
	}
	zones, err := h.deps.FetchZones(st.Token)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, zones)
}

func (h *Handler) ZoneImport(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	zoneID := r.PathValue("id")
	if zoneID == "" {
		jsonError(w, "zone id required", http.StatusBadRequest)
		return
	}
	st := h.deps.LoadCloudflareState()
	if st == nil || !st.Connected {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		DefaultType string   `json:"default_type"`
		DefaultRoot string   `json:"default_root"`
		DryRun      bool     `json:"dry_run"`
		Hostnames   []string `json:"hostnames"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.DefaultType == "" {
		req.DefaultType = "static"
	}
	switch req.DefaultType {
	case "static", "php", "proxy", "redirect":
	default:
		jsonError(w, "default_type must be one of: static, php, proxy, redirect", http.StatusBadRequest)
		return
	}
	records, err := h.deps.FetchDNSRecords(st.Token, zoneID)
	if err != nil {
		jsonError(w, "fetch records failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var whitelist map[string]bool
	if len(req.Hostnames) > 0 {
		whitelist = make(map[string]bool, len(req.Hostnames))
		for _, h := range req.Hostnames {
			whitelist[strings.ToLower(strings.TrimSuffix(h, "."))] = true
		}
	}
	seen := map[string]bool{}
	var hostnames []string
	for _, rec := range records {
		switch rec.Type {
		case "A", "AAAA", "CNAME":
		default:
			continue
		}
		host := strings.TrimSuffix(strings.ToLower(rec.Name), ".")
		if host == "" {
			continue
		}
		if strings.HasSuffix(host, ".cfargotunnel.com") {
			continue
		}
		if whitelist != nil && !whitelist[host] {
			continue
		}
		if !seen[host] {
			seen[host] = true
			hostnames = append(hostnames, host)
		}
	}
	existing := h.deps.ExistingDomains()
	webRoot := h.deps.WebRoot()
	if webRoot == "" {
		webRoot = "/var/www"
	}
	added := []string{}
	skipped := []string{}
	for _, host := range hostnames {
		if existing[host] {
			skipped = append(skipped, host)
			continue
		}
		if req.DryRun {
			added = append(added, host)
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
		if root != "" {
			os.MkdirAll(root, 0755)
		}
		h.deps.AddDomain(d)
		existing[host] = true
		added = append(added, host)
	}
	if len(added) > 0 && !req.DryRun {
		h.deps.NotifyDomainChange()
	}
	h.deps.RecordAudit(r, "cloudflare.zones.import", fmt.Sprintf("zone: %s, added: %d, skipped: %d", zoneID, len(added), len(skipped)), true)
	if req.DryRun {
		jsonResponse(w, map[string]any{
			"added": added, "skipped": skipped, "total": len(hostnames), "dry_run": true,
		})
		return
	}
	jsonResponse(w, map[string]any{
		"added": added, "skipped": skipped, "total": len(hostnames),
	})
}

// ── CF API helpers (self-contained, no admin dependency) ──

func ValidateToken(token, accountID string) (string, error) {
	return ValidateTokenWithClient(cfHTTPClient, token, accountID)
}

func ValidateTokenWithClient(client *http.Client, token, accountID string) (string, error) {
	req, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.Success {
		if len(result.Errors) > 0 {
			return "", fmt.Errorf("%s", result.Errors[0].Message)
		}
		return "", fmt.Errorf("token validation failed")
	}
	req2, _ := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/accounts/"+accountID, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := client.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	var acc struct {
		Success bool `json:"success"`
		Result  struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	json.NewDecoder(resp2.Body).Decode(&acc)
	if !acc.Success {
		return "", fmt.Errorf("account validation failed")
	}
	return acc.Result.Name, nil
}

func FetchZones(token string) ([]Zone, error) {
	return FetchZonesWithClient(cfHTTPClient, token)
}

func FetchZonesWithClient(client *http.Client, token string) ([]Zone, error) {
	const perPage = 50
	const maxPages = 50
	all := make([]Zone, 0, perPage)
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?per_page=%d&page=%d", perPage, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
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
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if !result.Success {
			if len(result.Errors) > 0 {
				return nil, fmt.Errorf("%s", result.Errors[0].Message)
			}
			return nil, fmt.Errorf("failed to fetch zones (page %d)", page)
		}
		for _, z := range result.Result {
			all = append(all, Zone{ID: z.ID, Name: z.Name, Status: z.Status, Plan: z.Plan.Name})
		}
		if result.ResultInfo.TotalPages == 0 || page >= result.ResultInfo.TotalPages {
			break
		}
		if len(result.Result) < perPage {
			break
		}
	}
	return all, nil
}

func FetchDNSRecords(token, zoneID string) ([]DNSRecord, error) {
	return FetchDNSRecordsWithClient(cfHTTPClient, token, zoneID)
}

func FetchDNSRecordsWithClient(client *http.Client, token, zoneID string) ([]DNSRecord, error) {
	req, _ := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/zones/"+zoneID+"/dns_records", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
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
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.Success {
		return nil, fmt.Errorf("failed to fetch DNS records")
	}
	records := make([]DNSRecord, len(result.Result))
	for i, r := range result.Result {
		records[i] = DNSRecord{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, TTL: r.TTL, Proxied: r.Proxied, Priority: r.Priority}
	}
	return records, nil
}

// PurgeCache purges cache for a URL or everything across all zones.
func PurgeCache(token, url string, everything bool) error {
	return PurgeCacheWithClient(cfHTTPClient, token, url, everything)
}

func PurgeCacheWithClient(client *http.Client, token, url string, everything bool) error {
	zones, err := FetchZonesWithClient(client, token)
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
		req, _ := http.NewRequest("POST", "https://api.cloudflare.com/client/v4/zones/"+zone.ID+"/purge_cache", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return nil
}

// isValidHostname checks if a hostname is valid.
func isValidHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}

// validateLocalTarget enforces a small whitelist of cloudflared service URLs.
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
