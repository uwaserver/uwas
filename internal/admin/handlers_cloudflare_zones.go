package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

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
