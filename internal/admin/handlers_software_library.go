package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"gopkg.in/yaml.v3"
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

func (s *Server) handleSoftwareBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	resp, err := createSoftwareBackup(inst)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "software.backup", inst.Name, true)
	jsonResponse(w, resp)
}

func (s *Server) handleSoftwareBackupAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := listSoftwareInstances()
	if err != nil {
		jsonError(w, "list software: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := softwareBackupAllResponse{Status: "created", Total: len(items)}
	for _, inst := range items {
		item, err := createSoftwareBackup(inst)
		if err != nil {
			item.Status = "failed"
			item.Name = inst.Name
			item.Output = err.Error()
			resp.Failed++
			resp.Status = "completed_with_errors"
		} else if item.Status == "skipped" {
			resp.Skipped++
		} else {
			resp.Created++
		}
		resp.Files = append(resp.Files, item.Files...)
		resp.Items = append(resp.Items, item)
	}
	if len(items) == 0 {
		resp.Status = "skipped"
	}
	s.recordAuditR(r, "software.backup_all", fmt.Sprintf("%d created, %d skipped, %d failed", resp.Created, resp.Skipped, resp.Failed), resp.Failed == 0)
	jsonResponse(w, resp)
}

func createSoftwareBackup(inst softwareInstance) (softwareBackupResponse, error) {
	volumes := collectSoftwareVolumeKeys(inst)
	if len(volumes) == 0 {
		return softwareBackupResponse{Status: "skipped", Name: inst.Name, Files: []string{}}, nil
	}
	backupDir := filepath.Join(softwareBackupRoot, inst.Name)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return softwareBackupResponse{Name: inst.Name}, fmt.Errorf("create backup directory: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	resp := softwareBackupResponse{Status: "created", Name: inst.Name}
	for _, key := range volumes {
		volume := softwareVolumeName(inst, key)
		fileName := sanitizeSoftwareName(inst.Name) + "-" + sanitizeSoftwareName(key) + "-" + stamp + ".tar.gz"
		out, err := runSoftwareDocker("run", "--rm",
			"-v", volume+":/data:ro",
			"-v", backupDir+":/backup",
			"alpine:3.20",
			"sh", "-c", "tar -czf /backup/"+fileName+" -C /data .")
		resp.Output += out
		if err != nil {
			return resp, fmt.Errorf("backup volume %s failed: %w\n%s", volume, err, out)
		}
		resp.Files = append(resp.Files, filepath.Join(backupDir, fileName))
	}
	return resp, nil
}

func updateSoftwareInstance(inst softwareInstance) (softwareUpdateResponse, error) {
	backup, err := createSoftwareBackup(inst)
	if err != nil {
		return softwareUpdateResponse{Name: inst.Name, Status: "failed"}, fmt.Errorf("backup before update failed: %w", err)
	}
	resp := softwareUpdateResponse{
		Status:       "updated",
		Name:         inst.Name,
		BackupStatus: backup.Status,
		BackupFiles:  backup.Files,
		Output:       backup.Output,
	}
	pullOut, err := runSoftwareComposeEnsuringInstalled(inst, "pull")
	resp.PullOutput = pullOut
	resp.Output += pullOut
	if err != nil {
		return resp, fmt.Errorf("docker compose pull failed: %w\n%s", err, pullOut)
	}
	upOut, err := runSoftwareComposeEnsuringInstalled(inst, "up", "-d", "--remove-orphans")
	resp.UpOutput = upOut
	resp.Output += upOut
	if err != nil {
		return resp, fmt.Errorf("docker compose up failed after pull: %w\n%s", err, upOut)
	}
	return resp, nil
}

func (s *Server) handleSoftwareBackupList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	items, err := listSoftwareBackups(inst)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleSoftwareBackupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	backupPath, _, err := resolveSoftwareBackupPath(inst, r.PathValue("backup"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Remove(backupPath); err != nil {
		if os.IsNotExist(err) {
			jsonError(w, "backup not found", http.StatusNotFound)
			return
		}
		jsonError(w, "delete backup: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "software.backup.delete", inst.Name+"/"+filepath.Base(backupPath), true)
	jsonResponse(w, map[string]string{"status": "deleted", "name": inst.Name, "backup": filepath.Base(backupPath)})
}

func (s *Server) handleSoftwareRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req softwareRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	backupPath, volumeKey, err := resolveSoftwareBackupPath(inst, req.Backup)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(backupPath); err != nil {
		jsonError(w, "backup not found: "+err.Error(), http.StatusNotFound)
		return
	}
	volume := softwareVolumeName(inst, volumeKey)
	var output strings.Builder
	if out, err := runSoftwareComposeEnsuringInstalled(inst, "stop"); err != nil {
		jsonError(w, "docker compose stop failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	} else {
		output.WriteString(out)
	}
	backupDir := filepath.Dir(backupPath)
	backupName := filepath.Base(backupPath)
	out, err := runSoftwareDocker("run", "--rm",
		"-v", volume+":/data",
		"-v", backupDir+":/backup:ro",
		"alpine:3.20",
		"sh", "-c", "rm -rf /data/* /data/.[!.]* /data/..?* 2>/dev/null || true; tar -xzf /backup/"+backupName+" -C /data")
	output.WriteString(out)
	if err != nil {
		jsonError(w, "restore volume "+volume+" failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	if out, err := runSoftwareComposeEnsuringInstalled(inst, "up", "-d"); err != nil {
		jsonError(w, "docker compose up failed after restore: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	} else {
		output.WriteString(out)
	}
	s.recordAuditR(r, "software.restore", inst.Name+"/"+backupName, true)
	jsonResponse(w, map[string]string{"status": "restored", "name": inst.Name, "volume": volume, "output": output.String()})
}

func (s *Server) handleSoftwareDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	inst, err := loadSoftwareInstance(r.PathValue("name"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	removeVolumes := r.URL.Query().Get("volumes") == "true"
	var backup softwareBackupResponse
	if removeVolumes {
		backup, err = createSoftwareBackup(inst)
		if err != nil {
			jsonError(w, "backup before volume removal failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	args := []string{"down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "-v")
	}
	out, err := runSoftwareComposeEnsuringInstalled(inst, args...)
	if err != nil {
		if !removeVolumes && isSoftwareComposeMissing(err) {
			if rmErr := removeSoftwareInstanceDir(inst); rmErr != nil {
				jsonError(w, "remove software metadata: "+rmErr.Error(), http.StatusInternalServerError)
				return
			}
			if inst.Domain != "" {
				s.detachSoftwareDomain(inst.Domain, inst.HostPort)
			}
			s.recordAuditR(r, "software.delete", inst.Name, true)
			jsonResponse(w, map[string]any{
				"status": "deleted",
				"name":   inst.Name,
				"output": strings.TrimSpace("Compose was not available, so UWAS removed the failed software record and left any Docker resources untouched.\n" + out),
			})
			return
		}
		jsonError(w, "docker compose down failed: "+err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	if err := removeSoftwareInstanceDir(inst); err != nil {
		jsonError(w, "remove software metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if inst.Domain != "" {
		s.detachSoftwareDomain(inst.Domain, inst.HostPort)
	}
	s.recordAuditR(r, "software.delete", inst.Name, true)
	resp := map[string]any{"status": "deleted", "name": inst.Name, "output": out}
	if removeVolumes {
		resp["backup_status"] = backup.Status
		resp["backup_files"] = backup.Files
	}
	jsonResponse(w, resp)
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

func (s *Server) allocateSoftwarePort(start int) int {
	if start <= 0 || start > 65535 {
		start = 3001
	}
	for port := start; port <= 65535 && port < start+2000; port++ {
		if s.softwarePortUnavailableReason(port) == "" {
			return port
		}
	}
	for port := 3001; port <= 65535; port++ {
		if s.softwarePortUnavailableReason(port) == "" {
			return port
		}
	}
	return 0
}

func (s *Server) softwarePortUnavailableReason(port int) string {
	if port <= 0 || port > 65535 {
		return "port out of range"
	}
	if reason := softwareConfiguredPortConflict(port, s.collectSoftwareReservedPorts()); reason != "" {
		return reason
	}
	if !softwarePortAvailable(port) {
		return "already bound on 127.0.0.1"
	}
	return ""
}

func (s *Server) collectSoftwareReservedPorts() map[int]string {
	used := map[int]string{}
	items, _ := listSoftwareInstances()
	for _, inst := range items {
		if inst.HasWeb && inst.HostPort > 0 {
			used[inst.HostPort] = "software " + inst.Name
		}
	}
	if s.appsMgr != nil {
		for _, inst := range s.appsMgr.Instances() {
			if inst.Port > 0 {
				used[inst.Port] = "application " + inst.Name
			}
		}
		if apps, _, err := s.appsMgr.Store().Load(); err == nil {
			for _, app := range apps {
				if app.Port > 0 {
					used[app.Port] = "application " + app.Name
				}
			}
		}
	}
	s.configMu.RLock()
	collectListenPort := func(addr, label string) {
		if port := portFromListenAddr(addr); port > 0 {
			used[port] = label
		}
	}
	collectListenPort(s.config.Global.HTTPListen, "global http listener")
	collectListenPort(s.config.Global.HTTPSListen, "global https listener")
	collectListenPort(s.config.Global.SFTPListen, "global sftp listener")
	collectListenPort(s.config.Global.Admin.Listen, "admin listener")
	collectListenPort(s.config.Global.MCP.Listen, "mcp listener")
	for _, d := range s.config.Domains {
		for _, up := range d.Proxy.Upstreams {
			if port := portFromLocalHTTPURL(up.Address); port > 0 {
				used[port] = "domain proxy " + d.Host
			}
		}
		if d.App.Port > 0 {
			used[d.App.Port] = "domain app " + d.Host
		}
		if port := portFromListenAddr(d.PHP.FPMAddress); port > 0 {
			used[port] = "php-fpm " + d.Host
		}
		for _, loc := range d.Locations {
			if port := portFromLocalHTTPURL(loc.ProxyPass); port > 0 {
				used[port] = "domain location " + d.Host
			}
		}
	}
	s.configMu.RUnlock()
	return used
}

func softwareConfiguredPortConflict(port int, used map[int]string) string {
	if label := used[port]; label != "" {
		return label
	}
	return ""
}

func optionalPositivePort(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port must be 1-65535")
	}
	return port, nil
}

func portFromListenAddr(addr string) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return 0
	}
	addr = strings.TrimPrefix(addr, "tcp:")
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

func portFromLocalHTTPURL(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return 0
	}
	if portStr := u.Port(); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err == nil && port > 0 && port <= 65535 {
			return port
		}
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
}

func findSoftwareTemplate(id string) *softwareTemplate {
	id = strings.TrimSpace(id)
	for i := range softwareTemplates {
		if softwareTemplates[i].ID == id {
			return &softwareTemplates[i]
		}
	}
	return nil
}

func listSoftwareInstances() ([]softwareInstance, error) {
	entries, err := os.ReadDir(softwareLibraryRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []softwareInstance{}, nil
		}
		return nil, err
	}
	var out []softwareInstance
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		inst, err := loadSoftwareInstance(entry.Name())
		if err == nil {
			out = append(out, inst)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadSoftwareInstance(name string) (softwareInstance, error) {
	name = sanitizeSoftwareName(name)
	if name == "" {
		return softwareInstance{}, fmt.Errorf("invalid software name")
	}
	path := filepath.Join(softwareLibraryRoot, name, "uwas-software.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return softwareInstance{}, err
	}
	var inst softwareInstance
	if err := yaml.Unmarshal(data, &inst); err != nil {
		return softwareInstance{}, err
	}
	return inst, nil
}

func saveSoftwareInstance(inst softwareInstance) error {
	data, err := yaml.Marshal(&inst)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(inst.Dir, "uwas-software.yaml"), data, 0600)
}

func updateSoftwareComposeDomain(inst softwareInstance) error {
	if inst.TemplateID != "n8n" || strings.TrimSpace(inst.ComposeFile) == "" {
		return nil
	}
	data, err := os.ReadFile(inst.ComposeFile)
	if err != nil {
		return err
	}
	webhook := ""
	if inst.Domain != "" {
		webhook = "https://" + inst.Domain + "/"
	}
	updated, changed := replaceComposeEnvironmentLine(string(data), "N8N_HOST", inst.Domain)
	updated, changedWebhook := replaceComposeEnvironmentLine(updated, "WEBHOOK_URL", webhook)
	if !changed && !changedWebhook {
		return nil
	}
	return os.WriteFile(inst.ComposeFile, []byte(updated), 0600)
}

func replaceComposeEnvironmentLine(data, key, value string) (string, bool) {
	lines := strings.SplitAfter(data, "\n")
	changed := false
	prefix := "- " + key + "="
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		newline := ""
		if strings.HasSuffix(trimmed, "\r\n") {
			newline = "\r\n"
		} else if strings.HasSuffix(trimmed, "\n") {
			newline = "\n"
		}
		replacement := indent + prefix + value + newline
		if line != replacement {
			lines[i] = replacement
			changed = true
		}
	}
	return strings.Join(lines, ""), changed
}

func removeSoftwareInstanceDir(inst softwareInstance) error {
	root, err := filepath.Abs(softwareLibraryRoot)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(inst.Dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to remove path outside software library: %s", inst.Dir)
	}
	return os.RemoveAll(dir)
}

func sanitizeSoftwareName(name string) string {
	return strings.Trim(appNameLike(name), "-_")
}

func appNameLike(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.' || r == ' ' || r == '/':
			b.WriteByte('-')
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

func fillSoftwareSecrets(templateID string, env map[string]string) {
	setDefault := func(k string) {
		if strings.TrimSpace(env[k]) == "" {
			env[k] = randomHex(16)
		}
	}
	switch templateID {
	case "adminer-postgres", "postgres":
		setDefault("POSTGRES_PASSWORD")
	case "mysql":
		setDefault("MYSQL_ROOT_PASSWORD")
		setDefault("MYSQL_PASSWORD")
	case "mariadb":
		setDefault("MARIADB_ROOT_PASSWORD")
		setDefault("MARIADB_PASSWORD")
	case "minio":
		if strings.TrimSpace(env["MINIO_ROOT_USER"]) == "" {
			env["MINIO_ROOT_USER"] = "admin"
		}
		setDefault("MINIO_ROOT_PASSWORD")
	case "n8n":
		setDefault("N8N_BASIC_AUTH_PASSWORD")
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "change-me-" + fmt.Sprint(n)
	}
	return hex.EncodeToString(buf)
}

func envValue(req softwareInstallRequest, key, fallback string) string {
	if v := strings.TrimSpace(req.Env[key]); v != "" {
		return v
	}
	return fallback
}
