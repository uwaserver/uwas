// Package domain provides admin API handlers for domain CRUD, unknown-domain
// management, and per-domain raw YAML editing. Extracted from the admin
// package following the established Deps-interface pattern.
package domain

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/domainutil"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/siteuser"
	"github.com/uwaserver/uwas/internal/webhook"
	"gopkg.in/yaml.v3"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequirePermission(w http.ResponseWriter, r *http.Request, perm auth.Permission) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	// Config access
	ConfigDomains() []config.Domain
	WebRoot() string
	DomainsDir() string
	ConfigPath() string
	// Config mutation (caller manages locking)
	LockConfig()
	UnlockConfig()
	ConfigPtr() *config.Config
	// File I/O
	PersistConfig()
	DomainFilePath(host string) (string, error)
	RemoveDomainFile(host string)
	AtomicWriteFile(path string, data []byte, perm os.FileMode) error
	// PHP manager (may be nil)
	PHPManager() *phpmanager.Manager
	CachePurgeByTag(tag string) int
	// Unknown host tracker
	UnknownHostAvailable() bool
	UnknownHostList() []any
	UnknownHostBlock(host string)
	UnknownHostUnblock(host string)
	UnknownHostDismiss(host string)
	// Webhook
	WebhookFire(event webhook.EventType, payload map[string]any)
	// Logging
	LogInfo(msg string, args ...any)
	LogWarn(msg string, args ...any)
	LogError(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Lifecycle
	NotifyDomainChange()
	Reload() error
	// Auth helpers
	UserFromContext(r *http.Request) (*auth.User, bool)
	CanManageDomain(user *auth.User, domain string) bool
}

// Handler holds domain admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a domain Handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// ── Helpers ──

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := parseInt(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func paginateSlice[T any](items []T, limit, offset int) ([]T, int) {
	total := len(items)
	if offset >= total {
		return []T{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total
}

// ── Domain validation ──

func validateDomainConfig(d *config.Domain, deps Deps) error {
	if err := config.ValidateDomain(d); err != nil {
		return err
	}
	webRoot := "/var/www"
	if w := deps.WebRoot(); w != "" {
		webRoot = w
	}
	if d.Type == "php" {
		mgr := deps.PHPManager()
		if mgr != nil {
			phpStatus := mgr.Status()
			activePHP := 0
			for _, p := range phpStatus {
				if !p.Disabled {
					activePHP++
				}
			}
			if activePHP == 0 {
				return fmt.Errorf("no active PHP versions available — install or enable PHP first")
			}
		}
	}
	if d.Root != "" && d.Type != "redirect" {
		if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
			return fmt.Errorf("root path must be under %s (got %s)", webRoot, d.Root)
		}
	}
	return nil
}

// ── Handlers ──

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	type domainInfo struct {
		Host           string   `json:"host"`
		MainHost       string   `json:"main_host,omitempty"`
		CanonicalHost  string   `json:"canonical_host,omitempty"`
		IP             string   `json:"ip,omitempty"`
		Aliases        []string `json:"aliases"`
		Type           string   `json:"type"`
		SSL            string   `json:"ssl"`
		ForceSSL       bool     `json:"force_ssl"`
		Root           string   `json:"root,omitempty"`
		CloudflareOnly bool     `json:"cloudflare_only"`
	}

	var allowedDomains map[string]bool
	if user, ok := h.deps.UserFromContext(r); ok {
		if user.Role != auth.RoleAdmin {
			allowedDomains = make(map[string]bool)
			for _, d := range user.Domains {
				allowedDomains[d] = true
			}
		}
	}

	domains := make([]domainInfo, 0)
	seenHosts := make(map[string]struct{})
	for _, d := range h.deps.ConfigDomains() {
		displayHost := domainutil.CanonicalDomainHostname(d.Host)
		if displayHost == "" {
			displayHost = domainutil.NormalizeDomainHostname(d.Host)
		}
		if domainutil.IsImplicitWWWRedirectForDomains(d, h.deps.ConfigDomains()) {
			continue
		}
		if _, ok := seenHosts[displayHost]; ok {
			continue
		}
		seenHosts[displayHost] = struct{}{}
		if allowedDomains != nil && !allowedDomains[d.Host] && !allowedDomains[displayHost] {
			continue
		}
		aliases := domainutil.PublicDomainAliases(d)
		if aliases == nil {
			aliases = []string{}
		}
		domains = append(domains, domainInfo{
			Host:           displayHost,
			MainHost:       domainutil.MainDomainHostname(d),
			CanonicalHost:  domainutil.NormalizeCanonicalHostPreference(d.CanonicalHost),
			IP:             d.IP,
			Aliases:        aliases,
			Type:           d.Type,
			SSL:            d.SSL.Mode,
			ForceSSL:       d.SSL.ForceSSL,
			Root:           d.Root,
			CloudflareOnly: d.Security.CloudflareOnly,
		})
	}
	limit, offset := parsePagination(r)
	domains, total := paginateSlice(domains, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  domains,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequirePermission(w, r, auth.PermDomainCreate) {
		return
	}
	// Check reseller permission on body host before processing
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		if qHost := r.URL.Query().Get("host"); qHost != "" {
			if !h.deps.CanManageDomain(user, qHost) {
				h.deps.RecordAudit(r, "domain.create", "domain: "+qHost+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var d config.Domain
	if err := json.Unmarshal(body, &d); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Parse alias options from raw body
	aliasOpts, err := parseAliasOptions(body)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Redirect domains cannot have aliases
	if d.Type == string(config.DomainTypeRedirect) && len(d.Aliases) > 0 {
		jsonError(w, "redirect domains cannot have aliases; create separate redirect domains instead", http.StatusBadRequest)
		return
	}
	if err := domainutil.ValidateRequestedDomainAliases(d.Host, d.Aliases); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	domainutil.NormalizeDomainHostnames(&d)
	if err := domainutil.ValidateRequestedDomainAliases(d.Host, d.Aliases); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Apply canonical preference
	var redirectAliases []string
	if d.Type != string(config.DomainTypeRedirect) {
		apex, _, ok := domainutil.ApexAndWWWHost(domainutil.NormalizeDomainHostname(d.Host))
		if ok {
			d.Host = apex
			if aliasOpts.canonicalHostSet {
				d.CanonicalHost = aliasOpts.canonicalHost
			} else {
				d.CanonicalHost = domainutil.NormalizeCanonicalHostPreference(d.CanonicalHost)
			}
			domainutil.NormalizeDomainHostnames(&d)
		}
	}
	if aliasOpts.redirect {
		redirectAliases = append(redirectAliases, d.Aliases...)
		d.Aliases = nil
	}
	redirectAliases = domainutil.UniqueNormalizedHostnames(redirectAliases)

	// Check reseller permission on parsed body host
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		if !h.deps.CanManageDomain(user, d.Host) {
			h.deps.RecordAudit(r, "domain.create", "domain: "+d.Host+" (forbidden)", false)
			jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
			return
		}
	}

	if d.Host == "" {
		jsonError(w, "host is required", http.StatusBadRequest)
		return
	}
	if !domainutil.IsValidHostname(d.Host) {
		jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
		return
	}
	if d.Type == "" {
		d.Type = "static"
	}
	if d.SSL.Mode == "" {
		d.SSL.Mode = "auto"
	}
	if !domainutil.DomainTypeUsesWebRoot(d.Type) {
		d.Root = ""
	}
	if err := validateDomainConfig(&d, h.deps); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	webRoot := h.deps.WebRoot()
	if webRoot == "" {
		webRoot = "/var/www"
	}
	if domainutil.DomainTypeUsesWebRoot(d.Type) && d.Root == "" {
		d.Root = filepath.Join(webRoot, d.Host, "public_html")
	}

	// Create web root + placeholder
	if d.Root != "" {
		if err := os.MkdirAll(d.Root, 0755); err != nil {
			h.deps.LogWarn("failed to create web root", "path", d.Root, "error", err)
		}
		idx := filepath.Join(d.Root, "index.html")
		if _, err := os.Stat(idx); os.IsNotExist(err) {
			placeholder := fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>%s</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0;background:#0f172a;color:#e2e8f0">
<div style="text-align:center"><h1>%s</h1><p style="color:#94a3b8">Site is ready. Upload your files via SFTP or place them in:<br><code>%s</code></p></div>
</body></html>`, d.Host, d.Host, d.Root)
			os.WriteFile(idx, []byte(placeholder), 0644)
		}
		if runtime.GOOS == "linux" {
			parentDir := filepath.Dir(d.Root)
			os.MkdirAll(filepath.Join(parentDir, "logs"), 0755)
			h.deps.LogInfo("created domain dirs", "root", d.Root)
		}
	}

	// PHP auto-assign
	if d.Type == "php" {
		mgr := h.deps.PHPManager()
		if mgr != nil {
			phpStatus := mgr.Status()
			if len(phpStatus) == 0 {
				_ = mgr.Detect()
				phpStatus = mgr.Status()
			}
			if len(phpStatus) > 0 {
				version := phpStatus[0].Version
				for _, st := range phpStatus {
					if strings.Contains(st.SAPI, "fpm") && st.Running {
						version = st.Version
						break
					}
				}
				if inst, err := mgr.AssignDomainWithRoot(d.Host, version, d.Root); err == nil {
					d.PHP.FPMAddress = inst.ListenAddr
					if err := mgr.StartDomain(d.Host); err != nil {
						h.deps.LogWarn("PHP auto-start failed", "domain", d.Host, "error", err)
					}
				}
			}
		}
	}

	if d.Type == "app" {
		jsonError(w, "type=app is no longer supported. Create the app via /api/v1/apps then add a type=proxy domain with apps://<name> upstream.", http.StatusBadRequest)
		return
	}

	// Check for duplicate hostnames
	h.deps.LockConfig()
	cfg := h.deps.ConfigPtr()
	// Remove implicit www redirect domains for this host
	domainutil.RemoveImplicitWWWRedirectDomains(&cfg.Domains, d.Host, -1)
	for _, existing := range cfg.Domains {
		if domainutil.CanonicalDomainHostname(existing.Host) == domainutil.CanonicalDomainHostname(d.Host) {
			h.deps.UnlockConfig()
			h.deps.RecordAudit(r, "domain.create", "domain: "+d.Host+" (duplicate)", false)
			jsonError(w, fmt.Sprintf("hostname %q is already configured", d.Host), http.StatusConflict)
			return
		}
	}
	// Check redirect aliases for conflicts
	for _, alias := range redirectAliases {
		for _, existing := range cfg.Domains {
			if domainutil.CanonicalDomainHostname(existing.Host) == domainutil.CanonicalDomainHostname(alias) {
				h.deps.UnlockConfig()
				jsonError(w, fmt.Sprintf("alias %q is already configured", alias), http.StatusConflict)
				return
			}
		}
	}
	cfg.Domains = append(cfg.Domains, d)
	// Add redirect alias domains
	if len(redirectAliases) > 0 {
		domainutil.UpsertCanonicalRedirectAliasDomains(&cfg.Domains, len(cfg.Domains)-1, redirectAliases, d.Host, aliasOpts.redirectCode, aliasOpts.preservePath)
	}
	h.deps.UnlockConfig()

	h.deps.RecordAudit(r, "domain.create", "domain: "+d.Host, true)
	h.deps.NotifyDomainChange()
	h.deps.WebhookFire(webhook.EventDomainAdd, map[string]any{"host": d.Host, "type": d.Type, "root": d.Root})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(d)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequirePermission(w, r, auth.PermDomainDelete) {
		return
	}
	if !h.deps.RequirePin(w, r) {
		return
	}
	host := domainutil.CanonicalDomainHostname(r.PathValue("host"))
	if r.URL.Query().Get("confirm") != "true" {
		jsonError(w, "missing confirmation: add ?confirm=true", http.StatusBadRequest)
		return
	}
	if host == "localhost" || host == "127.0.0.1" {
		jsonError(w, "cannot delete default domain: "+host, http.StatusForbidden)
		return
	}

	h.deps.LockConfig()
	cfg := h.deps.ConfigPtr()
	found := false
	var domainRoot string
	for i, d := range cfg.Domains {
		if domainutil.CanonicalDomainHostname(d.Host) == host {
			domainRoot = d.Root
			cfg.Domains = append(cfg.Domains[:i], cfg.Domains[i+1:]...)
			found = true
			break
		}
	}
	h.deps.UnlockConfig()

	if found && domainRoot != "" {
		pathsafe.InvalidateBase(domainRoot)
	}
	if !found {
		h.deps.RecordAudit(r, "domain.delete", "domain: "+host+" (not found)", false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	h.deps.RemoveDomainFile(host)

	if r.URL.Query().Get("cleanup") == "true" {
		mgr := h.deps.PHPManager()
		if mgr != nil {
			mgr.StopDomain(host)
			mgr.UnassignDomain(host)
		}
		h.deps.CachePurgeByTag("site:" + host)
		cronjob.RemoveByDomain(host)
		siteuser.DeleteUser(host)
	}

	h.deps.RecordAudit(r, "domain.delete", "domain: "+host, true)
	h.deps.NotifyDomainChange()
	h.deps.WebhookFire(webhook.EventDomainDelete, map[string]any{"host": host})

	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequirePermission(w, r, auth.PermDomainUpdate) {
		return
	}
	host := domainutil.CanonicalDomainHostname(r.PathValue("host"))

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var d config.Domain
	if err := json.Unmarshal(body, &d); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	aliasOpts, err := parseAliasOptions(body)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Reject aliases on redirect domain type before merge
	if len(d.Aliases) > 0 && h.domainTypeForHost(host) == string(config.DomainTypeRedirect) {
		h.deps.RecordAudit(r, "domain.update", "domain: "+host+" (redirect aliases rejected)", false)
		jsonError(w, "redirect domains cannot have aliases; create separate redirect domains instead", http.StatusBadRequest)
		return
	}
	domainutil.NormalizeDomainHostnames(&d)
	var redirectAliases []string
	if aliasOpts.redirect {
		redirectAliases = append(redirectAliases, d.Aliases...)
		d.Aliases = nil
	}

	if d.Host == "" {
		d.Host = host
	}
	domainutil.NormalizeDomainHostnames(&d)
	if d.Host != "" && !domainutil.IsValidHostname(d.Host) {
		jsonError(w, "invalid hostname", http.StatusBadRequest)
		return
	}

	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	replaceMode := r.URL.Query().Get("replace") == "true"

	hasSSLForce := false
	if rawSSL, ok := raw["ssl"]; ok {
		var sslRaw map[string]json.RawMessage
		if err := json.Unmarshal(rawSSL, &sslRaw); err == nil {
			_, hasSSLForce = sslRaw["force_ssl"]
		}
	}

	patchFields := config.DomainPatchFields{
		HasAliases:     rawHas(raw, "aliases"),
		HasLocations:   rawHas(raw, "locations"),
		HasBasicAuth:   rawHas(raw, "basic_auth"),
		HasSecurity:    rawHas(raw, "security"),
		HasCache:       rawHas(raw, "cache"),
		HasCompression: rawHas(raw, "compression"),
		HasHtaccess:    rawHas(raw, "htaccess"),
		HasSSL:         rawHas(raw, "ssl"),
		HasSSLForce:    hasSSLForce,
		HasResources:   rawHas(raw, "resources"),
		HasCanonical:   rawHas(raw, "canonical_host"),
	}

	h.deps.LockConfig()
	cfg := h.deps.ConfigPtr()
	found := false
	for i, existing := range cfg.Domains {
		if domainutil.CanonicalDomainHostname(existing.Host) == host {
			merged := config.MergeDomain(existing, d, patchFields, replaceMode)
			domainutil.NormalizeDomainHostnames(&merged)
			// Redirect domains cannot have aliases
			if merged.Type == string(config.DomainTypeRedirect) {
				if len(merged.Aliases) > 0 {
					merged.Aliases = nil
				}
			}
			if !domainutil.IsValidHostname(merged.Host) {
				h.deps.UnlockConfig()
				jsonError(w, "invalid hostname", http.StatusBadRequest)
				return
			}
			// Check duplicate on rename
			if merged.Host != host {
				// Reseller rename permission check
				if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
					if !h.deps.CanManageDomain(user, merged.Host) {
						h.deps.UnlockConfig()
						h.deps.RecordAudit(r, "domain.update", "domain: "+host+" (forbidden rename)", false)
						jsonError(w, "forbidden: cannot rename to this domain", http.StatusForbidden)
						return
					}
				}
				for j := range cfg.Domains {
					if j != i && domainutil.CanonicalDomainHostname(cfg.Domains[j].Host) == domainutil.CanonicalDomainHostname(merged.Host) {
						h.deps.UnlockConfig()
						h.deps.RecordAudit(r, "domain.update", "domain: "+host+" (duplicate rename)", false)
						jsonError(w, "domain already exists", http.StatusConflict)
						return
					}
				}
			}
			if err := config.ValidateDomainPartial(&merged); err != nil {
				h.deps.UnlockConfig()
				jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
				return
			}
			cfg.Domains[i] = merged
			d = merged
			found = true
			break
		}
	}
	h.deps.UnlockConfig()

	if !found {
		h.deps.RecordAudit(r, "domain.update", "domain: "+host+" (not found)", false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	h.deps.RecordAudit(r, "domain.update", "domain: "+host, true)
	h.deps.NotifyDomainChange()
	jsonResponse(w, d)
}

// aliasOptions holds alias redirect/canonical configuration parsed from request body.
type aliasOptions struct {
	redirect         bool
	redirectCode     int
	preservePath     bool
	canonicalHost    string
	canonicalHostSet bool
}

func parseAliasOptions(body []byte) (aliasOptions, error) {
	var raw struct {
		AliasMode         string `json:"alias_mode,omitempty"`
		AliasRedirectCode int    `json:"alias_redirect_code,omitempty"`
		AliasPreservePath *bool  `json:"alias_preserve_path,omitempty"`
		CanonicalHost     string `json:"canonical_host,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return aliasOptions{}, fmt.Errorf("invalid JSON")
	}
	mode := strings.ToLower(strings.TrimSpace(raw.AliasMode))
	opts := aliasOptions{preservePath: true}
	if raw.AliasPreservePath != nil {
		opts.preservePath = *raw.AliasPreservePath
	}
	if raw.CanonicalHost != "" {
		opts.canonicalHostSet = true
		ch, err := domainutil.NormalizeRequestedCanonicalHost(raw.CanonicalHost)
		if err != nil {
			return aliasOptions{}, err
		}
		opts.canonicalHost = ch
	}
	switch mode {
	case "", "alias", "redirect":
	default:
		return aliasOptions{}, fmt.Errorf("alias_mode must be redirect")
	}
	opts.redirect = true
	opts.redirectCode = raw.AliasRedirectCode
	if opts.redirectCode == 0 {
		opts.redirectCode = http.StatusMovedPermanently
	}
	if opts.redirectCode != http.StatusMovedPermanently && opts.redirectCode != http.StatusFound {
		return aliasOptions{}, fmt.Errorf("alias_redirect_code must be 301 or 302")
	}
	return opts, nil
}

func (h *Handler) domainTypeForHost(host string) string {
	host = domainutil.CanonicalDomainHostname(host)
	for _, d := range h.deps.ConfigDomains() {
		if domainutil.CanonicalDomainHostname(d.Host) == host {
			return d.Type
		}
	}
	return ""
}

func rawHas(raw map[string]json.RawMessage, key string) bool {
	_, ok := raw[key]
	return ok
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	host := domainutil.CanonicalDomainHostname(r.PathValue("host"))
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		if !h.deps.CanManageDomain(user, host) {
			jsonError(w, "forbidden: cannot view this domain", http.StatusForbidden)
			return
		}
	}
	for _, d := range h.deps.ConfigDomains() {
		if domainutil.CanonicalDomainHostname(d.Host) == host {
			out := d
			domainutil.NormalizeDomainHostnames(&out)
			out.Aliases = domainutil.PublicDomainAliases(out)
			jsonResponse(w, out)
			return
		}
	}
	jsonError(w, "domain not found", http.StatusNotFound)
}

func (h *Handler) UnknownList(w http.ResponseWriter, r *http.Request) {
	entries := h.deps.UnknownHostList()
	if entries == nil {
		entries = []any{}
	}
	jsonResponse(w, entries)
}

func (h *Handler) UnknownBlock(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		jsonError(w, "forbidden: admin access required", http.StatusForbidden)
		return
	}
	if !h.deps.UnknownHostAvailable() {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	h.deps.UnknownHostBlock(host)
	jsonResponse(w, map[string]string{"status": "blocked", "host": host})
}

func (h *Handler) UnknownUnblock(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		jsonError(w, "forbidden: admin access required", http.StatusForbidden)
		return
	}
	if !h.deps.UnknownHostAvailable() {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	h.deps.UnknownHostUnblock(host)
	jsonResponse(w, map[string]string{"status": "unblocked", "host": host})
}

func (h *Handler) UnknownDismiss(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		jsonError(w, "forbidden: admin access required", http.StatusForbidden)
		return
	}
	if !h.deps.UnknownHostAvailable() {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	h.deps.UnknownHostDismiss(host)
	jsonResponse(w, map[string]string{"status": "dismissed", "host": host})
}

func (h *Handler) UnknownAlias(w http.ResponseWriter, r *http.Request) {
	host := domainutil.NormalizeDomainHostname(r.PathValue("host"))
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		h.deps.RecordAudit(r, "unknown_domain.alias", "host: "+host+" (forbidden)", false)
		jsonError(w, "forbidden: admin access required", http.StatusForbidden)
		return
	}

	var req struct {
		Domain       string `json:"domain"`
		Mode         string `json:"mode,omitempty"`
		RedirectCode int    `json:"redirect_code,omitempty"`
		PreservePath *bool  `json:"preserve_path,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	target := domainutil.CanonicalDomainHostname(req.Domain)
	if target == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}

	redirectCode := req.RedirectCode
	if redirectCode == 0 {
		redirectCode = http.StatusMovedPermanently
	}

	h.deps.LockConfig()
	cfg := h.deps.ConfigPtr()
	targetIndex := -1
	for i, d := range cfg.Domains {
		if domainutil.CanonicalDomainHostname(d.Host) == target {
			targetIndex = i
			break
		}
	}
	if targetIndex == -1 {
		h.deps.UnlockConfig()
		jsonError(w, "target domain not found", http.StatusNotFound)
		return
	}

	// Check same-site www BEFORE conflict check
	if domainutil.CanonicalDomainHostname(host) == domainutil.CanonicalDomainHostname(cfg.Domains[targetIndex].Host) {
		// Remove host from target's aliases if present (same-site www)
		cfg.Domains[targetIndex].Aliases = domainutil.RemoveDomainAlias(cfg.Domains[targetIndex].Aliases, host)
		h.deps.UnlockConfig()
		h.deps.UnknownHostUnblock(host)
		h.deps.UnknownHostDismiss(host)
		h.deps.NotifyDomainChange()
		jsonResponse(w, map[string]string{"status": "already_primary", "host": host, "domain": target})
		return
	}

	// Check for conflict
	for i, d := range cfg.Domains {
		if i == targetIndex {
			continue
		}
		if domainutil.CanonicalDomainHostname(d.Host) == domainutil.CanonicalDomainHostname(host) {
			// Allow if this is a redirect alias pointing at the target (same-site www)
			if domainutil.IsCanonicalRedirectAliasDomain(d, host, cfg.Domains[targetIndex].Host) {
				continue
			}
			h.deps.UnlockConfig()
			jsonError(w, fmt.Sprintf("hostname %q is already configured", host), http.StatusConflict)
			return
		}
	}

	preservePath := true
	if req.PreservePath != nil {
		preservePath = *req.PreservePath
	}

	// Check if host is the www variant of target — if so, remove alias
	// from target domain and just unblock/dismiss.
	if domainutil.CanonicalDomainHostname(host) == domainutil.CanonicalDomainHostname(target) ||
		domainutil.ImplicitWWWHostname(domainutil.CanonicalDomainHostname(target)) == host {
		// Remove host from target's aliases (it's same-site)
		cfg.Domains[targetIndex].Aliases = domainutil.RemoveDomainAlias(cfg.Domains[targetIndex].Aliases, host)
		h.deps.UnlockConfig()
		h.deps.UnknownHostUnblock(host)
		h.deps.UnknownHostDismiss(host)
		h.deps.NotifyDomainChange()
		jsonResponse(w, map[string]string{"status": "already_primary", "host": host, "domain": target})
		return
	}

	redirectDomain := domainutil.NewCanonicalRedirectAliasDomain(host, cfg.Domains[targetIndex].Host, redirectCode, preservePath)
	cfg.Domains = append(cfg.Domains, redirectDomain)
	h.deps.UnlockConfig()

	h.deps.UnknownHostUnblock(host)
	h.deps.UnknownHostDismiss(host)
	h.deps.RecordAudit(r, "unknown_domain.alias_redirect", fmt.Sprintf("host: %s -> %s (%d)", host, target, redirectCode), true)
	h.deps.NotifyDomainChange()
	jsonResponse(w, map[string]any{"status": "redirect", "host": host, "domain": target, "redirect_code": redirectCode})
}

func (h *Handler) RawGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		if !h.deps.CanManageDomain(user, host) {
			jsonError(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	path, err := h.deps.DomainFilePath(host)
	if err == nil {
		data, err := os.ReadFile(path)
		if err == nil {
			jsonResponse(w, map[string]string{"content": string(data)})
			return
		}
	}
	for _, d := range h.deps.ConfigDomains() {
		if d.Host == host {
			data, err := yaml.Marshal(&d)
			if err != nil {
				jsonError(w, "failed to marshal", http.StatusInternalServerError)
				return
			}
			jsonResponse(w, map[string]string{"content": string(data)})
			return
		}
	}
	jsonError(w, "domain not found", http.StatusNotFound)
}

func (h *Handler) RawPut(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if user, ok := h.deps.UserFromContext(r); ok && user.Role != auth.RoleAdmin {
		if !h.deps.CanManageDomain(user, host) {
			jsonError(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	path, err := h.deps.DomainFilePath(host)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)
	var probe config.Domain
	if err := yaml.Unmarshal(data, &probe); err != nil {
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}
	tmpCfg := config.Config{
		Global:   config.GlobalConfig{LogLevel: "info", LogFormat: "json", Admin: config.AdminConfig{Listen: "127.0.0.1:9443"}, WebRoot: "/var/www"},
		Domains:  []config.Domain{probe},
	}
	if err := config.Validate(&tmpCfg); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		jsonError(w, "failed to save", http.StatusInternalServerError)
		return
	}
	if err := h.deps.AtomicWriteFile(path, data, 0600); err != nil {
		jsonError(w, "failed to save", http.StatusInternalServerError)
		return
	}
	if err := h.deps.Reload(); err != nil {
		jsonError(w, "domain saved but reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "saved"})
}
