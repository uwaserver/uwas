package admin

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/serverip"
)

// ============ Domain Debug ============

func (s *Server) handleDomainDebug(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	result := map[string]any{"host": host}

	// Config lookup
	s.configMu.RLock()
	var domainCfg *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == host {
			domainCfg = &s.config.Domains[i]
			break
		}
	}
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	if domainCfg == nil {
		result["error"] = "domain not found in config"
		result["configured"] = false
		jsonResponse(w, result)
		return
	}

	result["configured"] = true
	result["type"] = domainCfg.Type
	result["root"] = domainCfg.Root
	result["ssl_mode"] = domainCfg.SSL.Mode
	result["php_fpm_address"] = domainCfg.PHP.FPMAddress
	result["web_root_global"] = webRoot

	// Check if root directory exists
	if domainCfg.Root != "" {
		if info, err := os.Stat(domainCfg.Root); err != nil {
			result["root_exists"] = false
			result["root_error"] = err.Error()
		} else {
			result["root_exists"] = true
			result["root_is_dir"] = info.IsDir()
			// List files in root
			entries, _ := os.ReadDir(domainCfg.Root)
			var files []string
			for _, e := range entries {
				files = append(files, e.Name())
			}
			result["root_files"] = files
		}
	} else {
		result["root_exists"] = false
		result["root_error"] = "root is empty"
	}

	// Config match check
	result["in_config"] = true

	// PHP status
	if domainCfg.Type == "php" && s.phpMgr != nil {
		instances := s.phpMgr.GetDomainInstances()
		for _, inst := range instances {
			if inst.Domain == host {
				result["php_assigned"] = true
				result["php_version"] = inst.Version
				result["php_listen"] = inst.ListenAddr
				result["php_running"] = inst.Running
				result["php_pid"] = inst.PID
				break
			}
		}
		if result["php_assigned"] == nil {
			result["php_assigned"] = false
		}
	}

	// SSL/cert status
	if s.tlsMgr != nil {
		if certInfo := s.tlsMgr.CertStatus(host); certInfo != nil {
			result["cert_active"] = true
			result["cert_issuer"] = certInfo.Issuer
			result["cert_days_left"] = certInfo.DaysLeft
		} else {
			result["cert_active"] = false
		}
	} else {
		result["cert_active"] = false
	}

	jsonResponse(w, result)
}

// ============ Domain Health ============

func (s *Server) handleDomainHealth(w http.ResponseWriter, r *http.Request) {
	type healthTarget struct {
		Host       string
		ParentHost string
		Kind       string
		Domain     config.Domain
	}

	s.configMu.RLock()
	var allowedDomains map[string]bool
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			allowedDomains = make(map[string]bool, len(user.Domains))
			for _, d := range user.Domains {
				allowedDomains[normalizeDomainHostname(d)] = true
			}
		}
	}

	targets := make([]healthTarget, 0, len(s.config.Domains))
	seen := make(map[string]struct{})
	for _, d := range s.config.Domains {
		parentHost := normalizeDomainHostname(d.Host)
		if parentHost == "" {
			continue
		}
		if allowedDomains != nil && !allowedDomains[parentHost] {
			continue
		}
		for _, host := range domainHostnames(d) {
			if _, ok := seen[host]; ok {
				continue
			}
			kind := "primary"
			if host != parentHost {
				kind = "alias"
			}
			seen[host] = struct{}{}
			target := d
			target.Host = host
			targets = append(targets, healthTarget{
				Host:       host,
				ParentHost: parentHost,
				Kind:       kind,
				Domain:     target,
			})
		}
	}
	s.configMu.RUnlock()

	type healthResult struct {
		Host       string `json:"host"`
		ParentHost string `json:"parent_host,omitempty"`
		Kind       string `json:"kind,omitempty"` // "primary" or "alias"
		Target     string `json:"target,omitempty"`
		Status     string `json:"status"` // "up", "down", "error"
		Code       int    `json:"code"`
		Ms         int64  `json:"ms"`
		Error      string `json:"error,omitempty"`
	}

	results := make([]healthResult, len(targets))
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(idx int, target healthTarget) {
			defer wg.Done()
			dom := target.Domain
			hr := healthResult{
				Host:       target.Host,
				ParentHost: target.ParentHost,
				Kind:       target.Kind,
			}
			url, appErr := s.domainHealthURL(dom)
			hr.Target = url
			if appErr != "" {
				hr.Status = "down"
				hr.Error = appErr
				results[idx] = hr
				return
			}

			start := time.Now()
			resp, err := client.Get(url)
			hr.Ms = time.Since(start).Milliseconds()

			if err != nil {
				hr.Status = "down"
				hr.Error = err.Error()
			} else {
				resp.Body.Close()
				hr.Code = resp.StatusCode
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					hr.Status = "up"
				} else {
					hr.Status = "error"
				}
			}
			results[idx] = hr
		}(i, target)
	}
	wg.Wait()

	q := r.URL.Query()
	limit, offset := len(results), 0
	if _, hasLimit := q["limit"]; hasLimit {
		limit, offset = parsePagination(r)
	} else if _, hasOffset := q["offset"]; hasOffset {
		limit, offset = parsePagination(r)
	}
	items, total := paginateSlice(results, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) domainHealthURL(dom config.Domain) (string, string) {
	if appURL, appErr := s.appDomainHealthURL(dom); appURL != "" || appErr != "" {
		return appURL, appErr
	}
	scheme := "http"
	if dom.SSL.Mode == "auto" || dom.SSL.Mode == "manual" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/", scheme, dom.Host), ""
}

func (s *Server) appDomainHealthURL(dom config.Domain) (string, string) {
	if s.appsMgr == nil || len(dom.Proxy.Upstreams) == 0 {
		return "", ""
	}
	for _, upstream := range dom.Proxy.Upstreams {
		addr := strings.TrimSpace(upstream.Address)
		if addr == "" || !strings.HasPrefix(strings.ToLower(addr), "apps://") {
			continue
		}
		ref := strings.TrimPrefix(addr, "apps://")
		if idx := strings.IndexAny(ref, "/?#"); idx >= 0 {
			ref = ref[:idx]
		}
		if ref == "" {
			continue
		}
		name, port := ref, 0
		if colon := strings.LastIndex(ref, ":"); colon > 0 {
			if p, err := strconv.Atoi(ref[colon+1:]); err == nil {
				name, port = ref[:colon], p
			}
		}
		listen := ""
		if port > 0 {
			listen = s.appsMgr.ListenAddrForPort(name, port)
		} else {
			listen = s.appsMgr.ListenAddr(name)
		}
		if listen != "" {
			return "http://" + listen + "/", ""
		}
		return "", fmt.Sprintf("app upstream %q is not listening", ref)
	}
	return "", ""
}

func (s *Server) handleServerIPs(w http.ResponseWriter, r *http.Request) {
	ips := serverip.DetectAll()
	pub := serverip.PublicIP()
	jsonResponse(w, map[string]any{
		"ips":       ips,
		"public_ip": pub,
	})
}
