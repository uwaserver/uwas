package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if strings.HasPrefix(addr, "tcp:") {
		addr = strings.TrimPrefix(addr, "tcp:")
	}
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

func runSoftwareCompose(inst softwareInstance, args ...string) (string, error) {
	if err := ensureSoftwareComposeFileCompatible(inst); err != nil {
		return "", err
	}
	composeArgs := []string{"-p", inst.Project, "-f", inst.ComposeFile}
	composeArgs = append(composeArgs, args...)

	out, err := runSoftwareCommand(inst.Dir, "docker", append([]string{"compose"}, composeArgs...)...)
	if err == nil || (!isCommandNotFound(err) && !shouldFallbackDockerCompose(out)) {
		return out, err
	}
	fallbackOut, fallbackErr := runSoftwareCommand(inst.Dir, "docker-compose", composeArgs...)
	if fallbackErr == nil {
		return fallbackOut, nil
	}
	if isCommandNotFound(err) && isCommandNotFound(fallbackErr) {
		return "", softwareComposeMissingError{Reason: "Docker and Docker Compose are not installed or not available in PATH"}
	}
	if isCommandNotFound(fallbackErr) || shouldFallbackDockerCompose(fallbackOut) {
		return strings.TrimSpace(out + fallbackOut), softwareComposeMissingError{Reason: "Docker Compose is not installed or not available in PATH"}
	}
	return out + fallbackOut, fallbackErr
}

func ensureSoftwareComposeFileCompatible(inst softwareInstance) error {
	if strings.TrimSpace(inst.ComposeFile) == "" {
		return nil
	}
	data, err := os.ReadFile(inst.ComposeFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read docker-compose.yml: %w", err)
	}
	cleaned, changed := stripTopLevelComposeName(data)
	if !changed {
		return nil
	}
	if err := os.WriteFile(inst.ComposeFile, cleaned, 0600); err != nil {
		return fmt.Errorf("rewrite docker-compose.yml for legacy Compose compatibility: %w", err)
	}
	return nil
}

func stripTopLevelComposeName(data []byte) ([]byte, bool) {
	lines := strings.SplitAfter(string(data), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			changed = true
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return data, false
	}
	return []byte(strings.Join(out, "")), true
}

func runSoftwareComposeEnsuringInstalled(inst softwareInstance, args ...string) (string, error) {
	out, err := runSoftwareCompose(inst, args...)
	if err == nil || !isSoftwareComposeMissing(err) {
		return out, err
	}
	installOut, installErr := ensureSoftwareComposeAvailable()
	combined := strings.TrimSpace(installOut + "\n" + out)
	if installErr != nil {
		return combined, installErr
	}
	retryOut, retryErr := runSoftwareCompose(inst, args...)
	if combined != "" && retryOut != "" {
		retryOut = combined + "\n" + retryOut
	} else if combined != "" {
		retryOut = combined
	}
	return retryOut, retryErr
}

type softwareComposeMissingError struct {
	Reason string
}

func (e softwareComposeMissingError) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "Docker Compose is not installed or not available in PATH"
	}
	return reason + "; UWAS tried to use `docker compose` and legacy `docker-compose`. Install Docker + Compose, or let UWAS install them automatically on Debian/Ubuntu with: apt-get update && apt-get install -y docker.io docker-compose-plugin"
}

func isSoftwareComposeMissing(err error) bool {
	var missing softwareComposeMissingError
	return errors.As(err, &missing)
}

func ensureSoftwareComposeAvailable() (string, error) {
	softwareComposeSetupMu.Lock()
	defer softwareComposeSetupMu.Unlock()

	if out, err := probeSoftwareCompose(); err == nil {
		return out, nil
	}
	if _, err := softwareLookPath("apt-get"); err != nil {
		return "", softwareComposeMissingError{Reason: "Docker Compose is missing and apt-get is not available for automatic install"}
	}

	cmd := softwareComposeCommand("sh", "-c", strings.Join([]string{
		"apt-get update",
		"(apt-get install -y docker.io docker-compose-plugin || apt-get install -y docker.io docker-compose)",
		"(systemctl enable --now docker >/dev/null 2>&1 || service docker start >/dev/null 2>&1 || true)",
	}, " && "))
	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"NEEDRESTART_MODE=a",
		"APT_LISTCHANGES_FRONTEND=none",
		"DEBIAN_PRIORITY=critical",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("automatic Docker Compose install failed: %w", err)
	}
	probeOut, probeErr := probeSoftwareCompose()
	if probeErr != nil {
		return string(out) + probeOut, fmt.Errorf("Docker Compose install finished but Compose is still unavailable: %w", probeErr)
	}
	return string(out) + probeOut, nil
}

func probeSoftwareCompose() (string, error) {
	out, err := runSoftwareCommand("", "docker", "compose", "version")
	if err == nil {
		return out, nil
	}
	fallbackOut, fallbackErr := runSoftwareCommand("", "docker-compose", "version")
	if fallbackErr == nil {
		return fallbackOut, nil
	}
	if isCommandNotFound(err) && isCommandNotFound(fallbackErr) {
		return "", softwareComposeMissingError{Reason: "Docker and Docker Compose are not installed or not available in PATH"}
	}
	if isCommandNotFound(fallbackErr) || shouldFallbackDockerCompose(out) || shouldFallbackDockerCompose(fallbackOut) {
		return strings.TrimSpace(out + fallbackOut), softwareComposeMissingError{Reason: "Docker Compose is not installed or not available in PATH"}
	}
	return out + fallbackOut, fallbackErr
}

func runSoftwareDocker(args ...string) (string, error) {
	return runSoftwareCommand("", "docker", args...)
}

func runSoftwareCommand(dir, name string, args ...string) (string, error) {
	cmd := softwareComposeCommand(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func isCommandNotFound(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
}

func shouldFallbackDockerCompose(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "unknown shorthand flag: 'p'") ||
		strings.Contains(output, "unknown shorthand flag: \"p\"") ||
		strings.Contains(output, "docker: 'compose' is not a docker command") ||
		strings.Contains(output, "docker: \"compose\" is not a docker command") ||
		strings.Contains(output, "is not a docker command")
}

func collectSoftwareContainerStats(inst softwareInstance) ([]softwareContainerStat, error) {
	idsOut, err := runSoftwareCompose(inst, "ps", "-q")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(idsOut)
	if len(ids) == 0 {
		return []softwareContainerStat{}, nil
	}
	meta := collectSoftwareContainerMeta(inst)
	args := []string{"stats", "--no-stream", "--format", "{{json .}}"}
	args = append(args, ids...)
	statsOut, err := runSoftwareDocker(args...)
	if err != nil {
		return nil, err
	}
	var out []softwareContainerStat
	for _, line := range strings.Split(statsOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]string
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		id := raw["Container"]
		if id == "" {
			id = raw["ID"]
		}
		stat := softwareContainerStat{
			ID:        id,
			Name:      raw["Name"],
			CPUPct:    parseDockerPercent(raw["CPUPerc"]),
			MemoryPct: parseDockerPercent(raw["MemPerc"]),
			PIDs:      parseDockerInt(raw["PIDs"]),
		}
		stat.MemoryUsage, stat.MemoryLimit = parseDockerPair(raw["MemUsage"])
		stat.NetworkInput, stat.NetworkOutput = parseDockerPair(raw["NetIO"])
		stat.BlockInput, stat.BlockOutput = parseDockerPair(raw["BlockIO"])
		if m, ok := meta[stat.Name]; ok {
			stat.Service = m.Service
			stat.State = m.State
		}
		out = append(out, stat)
	}
	return out, nil
}

func collectSoftwareMonitor(inst softwareInstance) softwareMonitorResponse {
	resp := softwareMonitorResponse{Instance: inst}
	containers, err := collectSoftwareContainerStats(inst)
	if err == nil {
		resp.Containers = containers
	}
	resp.Volumes = collectSoftwareVolumes(inst)
	for _, c := range resp.Containers {
		resp.TotalCPUPct += c.CPUPct
		resp.TotalMemory += c.MemoryUsage
		resp.TotalMemoryLimit += c.MemoryLimit
		resp.TotalNetworkIn += c.NetworkInput
		resp.TotalNetworkOut += c.NetworkOutput
	}
	resp.TotalCPUPct = math.Round(resp.TotalCPUPct*100) / 100
	return resp
}

func collectSoftwareProcesses(inst softwareInstance) ([]softwareProcessInfo, error) {
	idsOut, err := runSoftwareCompose(inst, "ps", "-q")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(idsOut)
	if len(ids) == 0 {
		return []softwareProcessInfo{}, nil
	}
	meta := collectSoftwareContainerMeta(inst)
	out := []softwareProcessInfo{}
	for _, id := range ids {
		topOut, err := runSoftwareDocker("top", id)
		if err != nil {
			return out, fmt.Errorf("docker top %s failed: %w\n%s", id, err, topOut)
		}
		out = append(out, parseSoftwareTopOutput(id, meta[id], topOut)...)
	}
	return out, nil
}

func parseSoftwareTopOutput(containerID string, meta softwareContainerMeta, raw string) []softwareProcessInfo {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := []softwareProcessInfo{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		p := softwareProcessInfo{
			ContainerID:   containerID,
			ContainerName: meta.Name,
			Service:       meta.Service,
			User:          fields[0],
			PID:           fields[1],
		}
		if len(fields) > 2 {
			p.PPID = fields[2]
		}
		if len(fields) > 3 {
			p.CPU = fields[3]
		}
		if len(fields) > 4 {
			p.STime = fields[4]
		}
		if len(fields) > 5 {
			p.TTY = fields[5]
		}
		if len(fields) > 6 {
			p.Time = fields[6]
		}
		if len(fields) > 7 {
			p.Command = strings.Join(fields[7:], " ")
		} else if len(fields) > 2 {
			p.Command = strings.Join(fields[2:], " ")
		}
		out = append(out, p)
	}
	return out
}

type softwareContainerMeta struct {
	ID      string
	Name    string
	Service string
	State   string
}

func collectSoftwareContainerMeta(inst softwareInstance) map[string]softwareContainerMeta {
	out := map[string]softwareContainerMeta{}
	raw, err := runSoftwareCompose(inst, "ps", "--format", "json")
	if err != nil {
		return out
	}
	records := parseJSONRecords(raw)
	for _, rec := range records {
		name, _ := rec["Name"].(string)
		if name == "" {
			name, _ = rec["Names"].(string)
		}
		id, _ := rec["ID"].(string)
		if id == "" {
			id, _ = rec["Id"].(string)
		}
		service, _ := rec["Service"].(string)
		state, _ := rec["State"].(string)
		meta := softwareContainerMeta{ID: id, Name: name, Service: service, State: state}
		if name != "" {
			out[name] = meta
		}
		if id != "" {
			out[id] = meta
		}
	}
	return out
}

func parseJSONRecords(raw string) []map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr
	}
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func collectSoftwareVolumes(inst softwareInstance) []softwareVolumeInfo {
	keys := collectSoftwareVolumeKeys(inst)
	out := make([]softwareVolumeInfo, 0, len(keys))
	for _, key := range keys {
		name := softwareVolumeName(inst, key)
		info := softwareVolumeInfo{Name: name, Key: key, BackupHint: filepath.Join(softwareBackupRoot, inst.Name)}
		raw, err := runSoftwareDocker("volume", "inspect", name)
		if err == nil {
			var inspected []struct {
				Name       string `json:"Name"`
				Driver     string `json:"Driver"`
				Mountpoint string `json:"Mountpoint"`
				Scope      string `json:"Scope"`
			}
			if json.Unmarshal([]byte(raw), &inspected) == nil && len(inspected) > 0 {
				if inspected[0].Name != "" {
					info.Name = inspected[0].Name
				}
				info.Driver = inspected[0].Driver
				info.Mountpoint = inspected[0].Mountpoint
				info.Scope = inspected[0].Scope
			}
		}
		out = append(out, info)
	}
	return out
}

func collectSoftwareVolumeKeys(inst softwareInstance) []string {
	data, err := os.ReadFile(inst.ComposeFile)
	if err != nil {
		return nil
	}
	var doc struct {
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	keys := make([]string, 0, len(doc.Volumes))
	for key := range doc.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func listSoftwareBackups(inst softwareInstance) ([]softwareBackupInfo, error) {
	backupDir := filepath.Join(softwareBackupRoot, inst.Name)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []softwareBackupInfo{}, nil
		}
		return nil, err
	}
	var out []softwareBackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, softwareBackupInfo{
			Name:      entry.Name(),
			Path:      filepath.Join(backupDir, entry.Name()),
			VolumeKey: softwareBackupVolumeKey(inst, entry.Name()),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func resolveSoftwareBackupPath(inst softwareInstance, raw string) (string, string, error) {
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "." || name == "" || !strings.HasSuffix(name, ".tar.gz") {
		return "", "", fmt.Errorf("invalid backup")
	}
	backupDir, err := filepath.Abs(filepath.Join(softwareBackupRoot, inst.Name))
	if err != nil {
		return "", "", err
	}
	full, err := filepath.Abs(filepath.Join(backupDir, name))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(backupDir, full)
	if err != nil {
		return "", "", err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("backup path escapes software backup directory")
	}
	volumeKey := softwareBackupVolumeKey(inst, name)
	if volumeKey == "" {
		return "", "", fmt.Errorf("backup filename does not match a known software volume")
	}
	return full, volumeKey, nil
}

func softwareBackupVolumeKey(inst softwareInstance, fileName string) string {
	prefix := sanitizeSoftwareName(inst.Name) + "-"
	suffix := ".tar.gz"
	if !strings.HasPrefix(fileName, prefix) || !strings.HasSuffix(fileName, suffix) {
		return ""
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(fileName, prefix), suffix)
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 {
		return ""
	}
	candidate := rest[:idx]
	for _, key := range collectSoftwareVolumeKeys(inst) {
		if sanitizeSoftwareName(key) == candidate {
			return key
		}
	}
	return ""
}

func softwareVolumeName(inst softwareInstance, key string) string {
	return sanitizeSoftwareName(inst.Project) + "_" + sanitizeSoftwareName(key)
}

func parseDockerPercent(raw string) float64 {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "%"))
	if raw == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(raw, 64)
	return v
}

func parseDockerInt(raw string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(raw))
	return v
}

func parseDockerPair(raw string) (int64, int64) {
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return parseDockerBytes(raw), 0
	}
	return parseDockerBytes(parts[0]), parseDockerBytes(parts[1])
}

func parseDockerBytes(raw string) int64 {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, " ", ""))
	if raw == "" || raw == "--" {
		return 0
	}
	i := 0
	for i < len(raw) && (raw[i] == '.' || raw[i] == '-' || raw[i] == '+' || raw[i] >= '0' && raw[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0
	}
	num, _ := strconv.ParseFloat(raw[:i], 64)
	unit := strings.ToLower(raw[i:])
	mul := float64(1)
	switch unit {
	case "kb", "kib":
		mul = 1024
	case "mb", "mib":
		mul = 1024 * 1024
	case "gb", "gib":
		mul = 1024 * 1024 * 1024
	case "tb", "tib":
		mul = 1024 * 1024 * 1024 * 1024
	}
	return int64(num * mul)
}

func softwareComposeStatus(inst softwareInstance) string {
	out, err := runSoftwareCompose(inst, "ps", "-q")
	if err != nil {
		if isSoftwareComposeMissing(err) {
			return "needs-compose"
		}
		return "unknown"
	}
	if strings.TrimSpace(out) == "" {
		return "stopped"
	}
	return "running"
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

func composeHeader(_ string) string {
	return "services:\n"
}

func composeUptimeKuma(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  uptime-kuma:
    image: louislam/uptime-kuma:1
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    volumes:
      - uptime-kuma-data:/app/data
volumes:
  uptime-kuma-data:
`, req.HostPort, tpl.WebPort)
}

func composeN8N(req softwareInstallRequest, tpl softwareTemplate) string {
	webhook := ""
	if req.Domain != "" {
		webhook = "https://" + req.Domain + "/"
	}
	return composeHeader(req.Name) + fmt.Sprintf(`  n8n:
    image: n8nio/n8n:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    environment:
      - N8N_HOST=%s
      - WEBHOOK_URL=%s
      - N8N_BASIC_AUTH_ACTIVE=true
      - N8N_BASIC_AUTH_USER=%s
      - N8N_BASIC_AUTH_PASSWORD=%s
    volumes:
      - n8n-data:/home/node/.n8n
volumes:
  n8n-data:
`, req.HostPort, tpl.WebPort, req.Domain, webhook, envValue(req, "N8N_BASIC_AUTH_USER", "admin"), envValue(req, "N8N_BASIC_AUTH_PASSWORD", ""))
}

func composeVaultwarden(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  vaultwarden:
    image: vaultwarden/server:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    volumes:
      - vaultwarden-data:/data
volumes:
  vaultwarden-data:
`, req.HostPort, tpl.WebPort)
}

func composeGitea(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  gitea:
    image: gitea/gitea:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    volumes:
      - gitea-data:/data
volumes:
  gitea-data:
`, req.HostPort, tpl.WebPort)
}

func composePostgresAdminer(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      - POSTGRES_DB=%s
      - POSTGRES_USER=%s
      - POSTGRES_PASSWORD=%s
    volumes:
      - postgres-data:/var/lib/postgresql/data
  adminer:
    image: adminer:latest
    restart: unless-stopped
    depends_on:
      - postgres
    ports:
      - "127.0.0.1:%d:%d"
volumes:
  postgres-data:
`, envValue(req, "POSTGRES_DB", "app"), envValue(req, "POSTGRES_USER", "app"), envValue(req, "POSTGRES_PASSWORD", ""), req.HostPort, tpl.WebPort)
}

func composePostgres(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      - POSTGRES_DB=%s
      - POSTGRES_USER=%s
      - POSTGRES_PASSWORD=%s
    volumes:
      - postgres-data:/var/lib/postgresql/data
volumes:
  postgres-data:
`, envValue(req, "POSTGRES_DB", "app"), envValue(req, "POSTGRES_USER", "app"), envValue(req, "POSTGRES_PASSWORD", ""))
}

func composeMySQL(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  mysql:
    image: mysql:8
    restart: unless-stopped
    environment:
      - MYSQL_DATABASE=%s
      - MYSQL_USER=%s
      - MYSQL_PASSWORD=%s
      - MYSQL_ROOT_PASSWORD=%s
    volumes:
      - mysql-data:/var/lib/mysql
volumes:
  mysql-data:
`, envValue(req, "MYSQL_DATABASE", "app"), envValue(req, "MYSQL_USER", "app"), envValue(req, "MYSQL_PASSWORD", ""), envValue(req, "MYSQL_ROOT_PASSWORD", ""))
}

func composeMariaDB(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  mariadb:
    image: mariadb:11
    restart: unless-stopped
    environment:
      - MARIADB_DATABASE=%s
      - MARIADB_USER=%s
      - MARIADB_PASSWORD=%s
      - MARIADB_ROOT_PASSWORD=%s
    volumes:
      - mariadb-data:/var/lib/mysql
volumes:
  mariadb-data:
`, envValue(req, "MARIADB_DATABASE", "app"), envValue(req, "MARIADB_USER", "app"), envValue(req, "MARIADB_PASSWORD", ""), envValue(req, "MARIADB_ROOT_PASSWORD", ""))
}

func composeMinIO(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  minio:
    image: minio/minio:latest
    restart: unless-stopped
    command: server /data --console-address ":9001"
    ports:
      - "127.0.0.1:%d:9001"
    environment:
      - MINIO_ROOT_USER=%s
      - MINIO_ROOT_PASSWORD=%s
    volumes:
      - minio-data:/data
volumes:
  minio-data:
`, req.HostPort, envValue(req, "MINIO_ROOT_USER", "admin"), envValue(req, "MINIO_ROOT_PASSWORD", ""))
}

func composeRedis(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + `  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes"]
    volumes:
      - redis-data:/data
volumes:
  redis-data:
`
}

func composeMemcached(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + `  memcached:
    image: memcached:1.6-alpine
    restart: unless-stopped
    command: ["memcached", "-m", "128"]
`
}
