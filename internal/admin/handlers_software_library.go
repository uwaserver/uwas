package admin

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/config"
)

var (
	softwareLibraryRoot    = "/var/lib/uwas/software"
	softwareBackupRoot     = "/var/backups/uwas/software"
	softwareComposeCommand = exec.Command
	softwareLookPath       = exec.LookPath
	softwareInstallMu      sync.Mutex
	softwareComposeSetupMu sync.Mutex
	softwarePortAvailable  = func(port int) bool {
		if port <= 0 || port > 65535 {
			return false
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		_ = ln.Close()
		return true
	}
)

type softwareTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	HasWeb      bool              `json:"has_web"`
	WebService  string            `json:"web_service,omitempty"`
	WebPort     int               `json:"web_port,omitempty"`
	DefaultPort int               `json:"default_port,omitempty"`
	Internal    bool              `json:"internal,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	compose     func(softwareInstallRequest, softwareTemplate) string
}

type softwareInstance struct {
	Name        string `json:"name" yaml:"name"`
	TemplateID  string `json:"template_id" yaml:"template_id"`
	Template    string `json:"template" yaml:"template"`
	Category    string `json:"category" yaml:"category"`
	Dir         string `json:"dir" yaml:"dir"`
	ComposeFile string `json:"compose_file" yaml:"compose_file"`
	Project     string `json:"project" yaml:"project"`
	HasWeb      bool   `json:"has_web" yaml:"has_web"`
	WebService  string `json:"web_service,omitempty" yaml:"web_service,omitempty"`
	WebPort     int    `json:"web_port,omitempty" yaml:"web_port,omitempty"`
	HostPort    int    `json:"host_port,omitempty" yaml:"host_port,omitempty"`
	Domain      string `json:"domain,omitempty" yaml:"domain,omitempty"`
	Status      string `json:"status,omitempty" yaml:"-"`
}

type softwareInstallRequest struct {
	TemplateID string            `json:"template_id"`
	Name       string            `json:"name"`
	HostPort   int               `json:"host_port,omitempty"`
	Domain     string            `json:"domain,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

type softwareDomainRequest struct {
	Domain string `json:"domain"`
}

type softwarePortCheckResponse struct {
	Port          int    `json:"port"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
	SuggestedPort int    `json:"suggested_port,omitempty"`
}

type softwareContainerStat struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Service       string  `json:"service,omitempty"`
	State         string  `json:"state,omitempty"`
	CPUPct        float64 `json:"cpu_percent"`
	MemoryUsage   int64   `json:"memory_usage"`
	MemoryLimit   int64   `json:"memory_limit"`
	MemoryPct     float64 `json:"memory_percent"`
	NetworkInput  int64   `json:"network_input"`
	NetworkOutput int64   `json:"network_output"`
	BlockInput    int64   `json:"block_input"`
	BlockOutput   int64   `json:"block_output"`
	PIDs          int     `json:"pids"`
}

type softwareProcessInfo struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name,omitempty"`
	Service       string `json:"service,omitempty"`
	User          string `json:"user,omitempty"`
	PID           string `json:"pid"`
	PPID          string `json:"ppid,omitempty"`
	CPU           string `json:"cpu,omitempty"`
	STime         string `json:"stime,omitempty"`
	TTY           string `json:"tty,omitempty"`
	Time          string `json:"time,omitempty"`
	Command       string `json:"command"`
}

type softwareVolumeInfo struct {
	Name       string `json:"name"`
	Key        string `json:"key,omitempty"`
	Driver     string `json:"driver,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Scope      string `json:"scope,omitempty"`
	BackupHint string `json:"backup_hint,omitempty"`
}

type softwareMonitorResponse struct {
	Instance         softwareInstance        `json:"instance"`
	Containers       []softwareContainerStat `json:"containers"`
	Volumes          []softwareVolumeInfo    `json:"volumes"`
	TotalCPUPct      float64                 `json:"total_cpu_percent"`
	TotalMemory      int64                   `json:"total_memory"`
	TotalMemoryLimit int64                   `json:"total_memory_limit"`
	TotalNetworkIn   int64                   `json:"total_network_input"`
	TotalNetworkOut  int64                   `json:"total_network_output"`
}

type softwareMonitorSummary struct {
	Items            []softwareMonitorResponse `json:"items"`
	TotalCPUPct      float64                   `json:"total_cpu_percent"`
	TotalMemory      int64                     `json:"total_memory"`
	TotalMemoryLimit int64                     `json:"total_memory_limit"`
	TotalNetworkIn   int64                     `json:"total_network_input"`
	TotalNetworkOut  int64                     `json:"total_network_output"`
	ContainerCount   int                       `json:"container_count"`
	VolumeCount      int                       `json:"volume_count"`
}

type softwareBackupResponse struct {
	Status string   `json:"status"`
	Name   string   `json:"name"`
	Files  []string `json:"files"`
	Output string   `json:"output,omitempty"`
}

type softwareBackupAllResponse struct {
	Status  string                   `json:"status"`
	Items   []softwareBackupResponse `json:"items"`
	Files   []string                 `json:"files"`
	Total   int                      `json:"total"`
	Created int                      `json:"created"`
	Skipped int                      `json:"skipped"`
	Failed  int                      `json:"failed"`
}

type softwareUpdateResponse struct {
	Status       string   `json:"status"`
	Name         string   `json:"name"`
	BackupStatus string   `json:"backup_status"`
	BackupFiles  []string `json:"backup_files"`
	PullOutput   string   `json:"pull_output,omitempty"`
	UpOutput     string   `json:"up_output,omitempty"`
	Output       string   `json:"output,omitempty"`
}

type softwareUpdateAllResponse struct {
	Status  string                   `json:"status"`
	Items   []softwareUpdateResponse `json:"items"`
	Total   int                      `json:"total"`
	Updated int                      `json:"updated"`
	Skipped int                      `json:"skipped"`
	Failed  int                      `json:"failed"`
}

type softwareBackupInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	VolumeKey string `json:"volume_key,omitempty"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}

type softwareRestoreRequest struct {
	Backup string `json:"backup"`
}

var softwareTemplates = []softwareTemplate{
	{
		ID:          "uptime-kuma",
		Name:        "Uptime Kuma",
		Description: "Status pages and uptime monitoring.",
		Category:    "Monitoring",
		HasWeb:      true,
		WebService:  "uptime-kuma",
		WebPort:     3001,
		DefaultPort: 3001,
		compose:     composeUptimeKuma,
	},
	{
		ID:          "n8n",
		Name:        "n8n",
		Description: "Workflow automation with optional public webhook URL.",
		Category:    "Automation",
		HasWeb:      true,
		WebService:  "n8n",
		WebPort:     5678,
		DefaultPort: 5678,
		Env:         map[string]string{"N8N_BASIC_AUTH_USER": "admin"},
		compose:     composeN8N,
	},
	{
		ID:          "vaultwarden",
		Name:        "Vaultwarden",
		Description: "Lightweight Bitwarden-compatible password vault.",
		Category:    "Security",
		HasWeb:      true,
		WebService:  "vaultwarden",
		WebPort:     80,
		DefaultPort: 8088,
		compose:     composeVaultwarden,
	},
	{
		ID:          "gitea",
		Name:        "Gitea",
		Description: "Self-hosted Git service.",
		Category:    "Development",
		HasWeb:      true,
		WebService:  "gitea",
		WebPort:     3000,
		DefaultPort: 3000,
		compose:     composeGitea,
	},
	{
		ID:          "adminer-postgres",
		Name:        "Postgres + Adminer",
		Description: "Internal Postgres database with a web Adminer UI.",
		Category:    "Database",
		HasWeb:      true,
		WebService:  "adminer",
		WebPort:     8080,
		DefaultPort: 8081,
		Env:         map[string]string{"POSTGRES_DB": "app", "POSTGRES_USER": "app"},
		compose:     composePostgresAdminer,
	},
	{
		ID:          "postgres",
		Name:        "Postgres",
		Description: "Internal PostgreSQL database. Not exposed by default.",
		Category:    "Database",
		Internal:    true,
		Env:         map[string]string{"POSTGRES_DB": "app", "POSTGRES_USER": "app"},
		compose:     composePostgres,
	},
	{
		ID:          "mysql",
		Name:        "MySQL",
		Description: "Internal MySQL database. Not exposed by default.",
		Category:    "Database",
		Internal:    true,
		Env:         map[string]string{"MYSQL_DATABASE": "app", "MYSQL_USER": "app"},
		compose:     composeMySQL,
	},
	{
		ID:          "mariadb",
		Name:        "MariaDB",
		Description: "Internal MariaDB database. Not exposed by default.",
		Category:    "Database",
		Internal:    true,
		Env:         map[string]string{"MARIADB_DATABASE": "app", "MARIADB_USER": "app"},
		compose:     composeMariaDB,
	},
	{
		ID:          "minio",
		Name:        "MinIO",
		Description: "S3-compatible object storage with console UI.",
		Category:    "Storage",
		HasWeb:      true,
		WebService:  "minio",
		WebPort:     9001,
		DefaultPort: 9001,
		compose:     composeMinIO,
	},
	{
		ID:          "redis",
		Name:        "Redis",
		Description: "Internal Redis service for queues/cache. Not exposed by default.",
		Category:    "Infrastructure",
		Internal:    true,
		compose:     composeRedis,
	},
	{
		ID:          "memcached",
		Name:        "Memcached",
		Description: "Internal Memcached service for application caching.",
		Category:    "Infrastructure",
		Internal:    true,
		compose:     composeMemcached,
	},
}

func (s *Server) handleSoftwareTemplateList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out := make([]softwareTemplate, len(softwareTemplates))
	copy(out, softwareTemplates)
	for i := range out {
		out[i].compose = nil
	}
	jsonResponse(w, map[string]any{"items": out, "total": len(out), "limit": len(out), "offset": 0})
}

func (s *Server) handleSoftwareInstanceList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := listSoftwareInstances()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for i := range items {
		items[i].Status = softwareComposeStatus(items[i])
	}
	jsonResponse(w, map[string]any{"items": items, "total": len(items), "limit": len(items), "offset": 0})
}

func (s *Server) handleSoftwarePortCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	port, err := optionalPositivePort(r.URL.Query().Get("port"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	defaultPort, err := optionalPositivePort(r.URL.Query().Get("default_port"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	softwareInstallMu.Lock()
	defer softwareInstallMu.Unlock()
	if port == 0 {
		suggested := s.allocateSoftwarePort(defaultPort)
		if suggested == 0 {
			jsonResponse(w, softwarePortCheckResponse{Available: false, Reason: "no available host port found"})
			return
		}
		jsonResponse(w, softwarePortCheckResponse{Port: suggested, Available: true, SuggestedPort: suggested})
		return
	}
	reason := s.softwarePortUnavailableReason(port)
	resp := softwarePortCheckResponse{Port: port, Available: reason == ""}
	if reason != "" {
		resp.Reason = reason
		resp.SuggestedPort = s.allocateSoftwarePort(port + 1)
	}
	jsonResponse(w, resp)
}

func (s *Server) handleSoftwareInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req softwareInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	tpl := findSoftwareTemplate(req.TemplateID)
	if tpl == nil {
		jsonError(w, "unknown software template: "+req.TemplateID, http.StatusBadRequest)
		return
	}
	req.Name = sanitizeSoftwareName(req.Name)
	if req.Name == "" {
		req.Name = sanitizeSoftwareName(tpl.ID)
	}
	softwareInstallMu.Lock()
	defer softwareInstallMu.Unlock()
	if req.HostPort < 0 || req.HostPort > 65535 {
		jsonError(w, "host_port must be 1-65535", http.StatusBadRequest)
		return
	}
	if !tpl.HasWeb {
		req.HostPort = 0
		req.Domain = ""
	} else if req.HostPort == 0 {
		req.HostPort = s.allocateSoftwarePort(tpl.DefaultPort)
		if req.HostPort == 0 {
			jsonError(w, "no available host port found", http.StatusConflict)
			return
		}
	} else if reason := s.softwarePortUnavailableReason(req.HostPort); reason != "" {
		jsonError(w, fmt.Sprintf("host_port %d is not available: %s", req.HostPort, reason), http.StatusConflict)
		return
	}
	if strings.TrimSpace(req.Domain) != "" {
		req.Domain = normalizeDomainHostname(req.Domain)
		if req.Domain == "" || !isValidHostname(req.Domain) {
			jsonError(w, "invalid domain", http.StatusBadRequest)
			return
		}
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	fillSoftwareSecrets(tpl.ID, req.Env)

	dir := filepath.Join(softwareLibraryRoot, req.Name)
	metaPath := filepath.Join(dir, "uwas-software.yaml")
	composePath := filepath.Join(dir, "docker-compose.yml")
	if _, err := os.Stat(metaPath); err == nil {
		jsonError(w, "software instance already exists: "+req.Name, http.StatusConflict)
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		jsonError(w, "create software directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	compose := tpl.compose(req, *tpl)
	if err := os.WriteFile(composePath, []byte(compose), 0600); err != nil {
		jsonError(w, "write docker-compose.yml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	inst := softwareInstance{
		Name:        req.Name,
		TemplateID:  tpl.ID,
		Template:    tpl.Name,
		Category:    tpl.Category,
		Dir:         dir,
		ComposeFile: composePath,
		Project:     "uwas-" + req.Name,
		HasWeb:      tpl.HasWeb,
		WebService:  tpl.WebService,
		WebPort:     tpl.WebPort,
		HostPort:    req.HostPort,
		Domain:      req.Domain,
	}
	if err := saveSoftwareInstance(inst); err != nil {
		jsonError(w, "write metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out, err := runSoftwareComposeEnsuringInstalled(inst, "up", "-d")
	if err != nil {
		_ = removeSoftwareInstanceDir(inst)
		jsonError(w, "docker compose up failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	if req.Domain != "" {
		if err := s.attachSoftwareDomain(req.Domain, req.HostPort); err != nil {
			jsonError(w, "compose installed but domain attach failed: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	inst.Status = "running"
	s.recordAuditR(r, "software.install", req.Name+" ("+tpl.Name+")", true)
	jsonResponse(w, inst)
}

func (s *Server) handleSoftwareStart(w http.ResponseWriter, r *http.Request) {
	s.handleSoftwareComposeAction(w, r, "start", []string{"up", "-d"})
}

func (s *Server) handleSoftwareStop(w http.ResponseWriter, r *http.Request) {
	s.handleSoftwareComposeAction(w, r, "stop", []string{"stop"})
}

func (s *Server) handleSoftwareRestart(w http.ResponseWriter, r *http.Request) {
	s.handleSoftwareComposeAction(w, r, "restart", []string{"restart"})
}

func (s *Server) handleSoftwareUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	resp, err := updateSoftwareInstance(inst)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "software.update", inst.Name, true)
	jsonResponse(w, resp)
}

func (s *Server) handleSoftwareUpdateAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := listSoftwareInstances()
	if err != nil {
		jsonError(w, "list software: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := softwareUpdateAllResponse{Status: "updated", Total: len(items)}
	for _, inst := range items {
		item, err := updateSoftwareInstance(inst)
		if err != nil {
			item.Status = "failed"
			item.Name = inst.Name
			item.Output = err.Error()
			resp.Failed++
			resp.Status = "completed_with_errors"
		} else if item.Status == "skipped" {
			resp.Skipped++
		} else {
			resp.Updated++
		}
		resp.Items = append(resp.Items, item)
	}
	if len(items) == 0 {
		resp.Status = "skipped"
	}
	s.recordAuditR(r, "software.update_all", fmt.Sprintf("%d updated, %d skipped, %d failed", resp.Updated, resp.Skipped, resp.Failed), resp.Failed == 0)
	jsonResponse(w, resp)
}

func (s *Server) handleSoftwareDomainConnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	if !inst.HasWeb || inst.HostPort == 0 {
		jsonError(w, "software instance has no web service to expose", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req softwareDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	host := normalizeDomainHostname(req.Domain)
	if host == "" || !isValidHostname(host) {
		jsonError(w, "invalid domain", http.StatusBadRequest)
		return
	}
	if strings.EqualFold(inst.Domain, host) {
		inst.Domain = host
		jsonResponse(w, inst)
		return
	}
	oldDomain := inst.Domain
	if err := s.attachSoftwareDomain(host, inst.HostPort); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	inst.Domain = host
	if err := updateSoftwareComposeDomain(inst); err != nil {
		s.detachSoftwareDomain(host, inst.HostPort)
		inst.Domain = oldDomain
		jsonError(w, "update compose domain: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := saveSoftwareInstance(inst); err != nil {
		s.detachSoftwareDomain(host, inst.HostPort)
		inst.Domain = oldDomain
		_ = updateSoftwareComposeDomain(inst)
		jsonError(w, "write metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if oldDomain != "" {
		s.detachSoftwareDomain(oldDomain, inst.HostPort)
	}
	s.recordAuditR(r, "software.domain.connect", inst.Name+" -> "+host, true)
	jsonResponse(w, inst)
}

func (s *Server) handleSoftwareDomainDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	oldDomain := inst.Domain
	if oldDomain == "" {
		jsonResponse(w, inst)
		return
	}
	inst.Domain = ""
	if err := updateSoftwareComposeDomain(inst); err != nil {
		inst.Domain = oldDomain
		jsonError(w, "update compose domain: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := saveSoftwareInstance(inst); err != nil {
		inst.Domain = oldDomain
		_ = updateSoftwareComposeDomain(inst)
		jsonError(w, "write metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.detachSoftwareDomain(oldDomain, inst.HostPort)
	s.recordAuditR(r, "software.domain.disconnect", inst.Name+" -> "+oldDomain, true)
	jsonResponse(w, inst)
}

func (s *Server) handleSoftwareLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	out, err := runSoftwareCompose(inst, "logs", "--tail", "200")
	if err != nil {
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"logs": out})
}

func (s *Server) handleSoftwareMonitorSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := listSoftwareInstances()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	summary := softwareMonitorSummary{}
	for _, inst := range items {
		mon := collectSoftwareMonitor(inst)
		summary.Items = append(summary.Items, mon)
		summary.TotalCPUPct += mon.TotalCPUPct
		summary.TotalMemory += mon.TotalMemory
		summary.TotalMemoryLimit += mon.TotalMemoryLimit
		summary.TotalNetworkIn += mon.TotalNetworkIn
		summary.TotalNetworkOut += mon.TotalNetworkOut
		summary.ContainerCount += len(mon.Containers)
		summary.VolumeCount += len(mon.Volumes)
	}
	summary.TotalCPUPct = math.Round(summary.TotalCPUPct*100) / 100
	jsonResponse(w, summary)
}

func (s *Server) handleSoftwareMonitor(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, collectSoftwareMonitor(inst))
}

func (s *Server) handleSoftwareProcesses(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	items, err := collectSoftwareProcesses(inst)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleSoftwareComposeAction(w http.ResponseWriter, r *http.Request, action string, args []string) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	out, err := runSoftwareComposeEnsuringInstalled(inst, args...)
	if err != nil {
		jsonError(w, "docker compose "+action+" failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "software."+action, inst.Name, true)
	jsonResponse(w, map[string]string{"status": action, "output": out})
}

func (s *Server) attachSoftwareDomain(host string, hostPort int) error {
	if host == "" || hostPort == 0 {
		return nil
	}
	s.configMu.Lock()
	if conflict := findDomainHostnameConflict(s.config.Domains, -1, host); conflict != "" {
		s.configMu.Unlock()
		return fmt.Errorf("domain %s already exists as %s", host, conflict)
	}
	d := config.Domain{
		Host: host,
		Type: "proxy",
		Proxy: config.ProxyConfig{
			Upstreams:             []config.Upstream{{Address: fmt.Sprintf("http://127.0.0.1:%d", hostPort)}},
			WebSocket:             true,
			AllowPrivateUpstreams: true,
		},
		SSL: config.SSLConfig{Mode: "auto"},
	}
	s.config.Domains = append(s.config.Domains, d)
	s.configMu.Unlock()
	s.notifyDomainChange()
	return nil
}

func (s *Server) detachSoftwareDomain(host string, hostPort int) {
	host = normalizeDomainHostname(host)
	if host == "" || hostPort == 0 {
		return
	}
	upstream := fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	s.configMu.Lock()
	idx := -1
	for i, d := range s.config.Domains {
		if !strings.EqualFold(d.Host, host) || d.Type != "proxy" || len(d.Proxy.Upstreams) != 1 {
			continue
		}
		if d.Proxy.Upstreams[0].Address == upstream {
			idx = i
			break
		}
	}
	if idx >= 0 {
		s.config.Domains = append(s.config.Domains[:idx], s.config.Domains[idx+1:]...)
	}
	s.configMu.Unlock()
	if idx >= 0 {
		s.notifyDomainChange()
	}
}
