package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	cfintegration "github.com/uwaserver/uwas/internal/cloudflare"
)

// --- Cloudflare Integration ---

// cfHTTPClient is used for all outbound Cloudflare API calls. http.DefaultClient
// has no timeout, so a stalled/hostile API endpoint would hang the goroutine
// indefinitely; this bounds every call.
var cfHTTPClient = &http.Client{Timeout: 30 * time.Second}

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
	// Connected account identity, token mask, and tunnel counts are global
	// provider state and should not leak to non-admin tenants.
	if !s.requireAdmin(w, r) {
		return
	}
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

	resp, err := cfHTTPClient.Do(req)
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

	resp2, err := cfHTTPClient.Do(req2)
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

		resp, err := cfHTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Read and discard response body to ensure connection reuse
		io.Copy(io.Discard, resp.Body)
	}

	return nil
}

// maskCloudflareToken returns the last 4 chars of the token prefixed with stars.
func maskCloudflareToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}
