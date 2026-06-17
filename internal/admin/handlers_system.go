package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/doctor"
	"github.com/uwaserver/uwas/internal/filemanager"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/selfupdate"
	"github.com/uwaserver/uwas/internal/services"
)

var (
	systemExecCommandMu sync.RWMutex
	systemExecCommand   = exec.Command
	doctorRun           = doctor.Run
)

func newSystemExecCommand(name string, args ...string) *exec.Cmd {
	systemExecCommandMu.RLock()
	fn := systemExecCommand
	systemExecCommandMu.RUnlock()
	return fn(name, args...)
}

func setSystemExecCommand(fn func(string, ...string) *exec.Cmd) func() {
	systemExecCommandMu.Lock()
	orig := systemExecCommand
	systemExecCommand = fn
	systemExecCommandMu.Unlock()
	return func() {
		systemExecCommandMu.Lock()
		systemExecCommand = orig
		systemExecCommandMu.Unlock()
	}
}

// ============ Self-Update ============

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := selfupdate.CheckUpdate(build.Version)
	if err != nil {
		jsonErrorCause(w, "update check failed", err, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, info)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	info, err := selfupdate.CheckUpdate(build.Version)
	if err != nil {
		jsonErrorCause(w, "update check failed", err, http.StatusInternalServerError)
		return
	}
	if !info.UpdateAvail {
		jsonResponse(w, map[string]string{"status": "up-to-date", "version": info.CurrentVersion})
		return
	}
	if err := selfupdate.Update(info.DownloadURL); err != nil {
		jsonErrorCause(w, "update failed", err, http.StatusInternalServerError)
		return
	}
	s.logger.Info("UWAS updated", "from", info.CurrentVersion, "to", info.LatestVersion)
	jsonResponse(w, map[string]string{
		"status":  "updated",
		"from":    info.CurrentVersion,
		"to":      info.LatestVersion,
		"message": "Restarting UWAS...",
	})

	// Auto-restart after response is sent. RestartSelf tries systemctl first,
	// then syscall.Exec; if both fail, the new binary is on disk but the running
	// process is still the old one, so we log loudly to surface the situation.
	go func() {
		time.Sleep(500 * time.Millisecond) // let response flush
		if err := selfupdate.RestartSelf(); err != nil {
			s.logger.Error("UWAS auto-restart failed after self-update", "error", err.Error(),
				"hint", "new binary is installed; restart manually: 'sudo systemctl restart uwas' or 'sudo uwas restart'")
		}
	}()
}

// ============ System Services ============

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	svcs := services.ListServices()
	if svcs == nil {
		svcs = []services.Service{}
	}
	limit, offset := parsePagination(r)
	svcs, total := paginateSlice(svcs, limit, offset)
	jsonResponse(w, map[string]any{"items": svcs, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleServiceStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := services.StartService(name); err != nil {
		jsonErrorCause(w, "service start failed", err, http.StatusInternalServerError)
		return
	}
	s.logger.Info("service started", "name", name)
	jsonResponse(w, map[string]string{"status": "started", "name": name})
}

func (s *Server) handleServiceStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := services.StopService(name); err != nil {
		jsonErrorCause(w, "service stop failed", err, http.StatusInternalServerError)
		return
	}
	s.logger.Info("service stopped", "name", name)
	jsonResponse(w, map[string]string{"status": "stopped", "name": name})
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := services.RestartService(name); err != nil {
		jsonErrorCause(w, "service restart failed", err, http.StatusInternalServerError)
		return
	}
	s.logger.Info("service restarted", "name", name)
	jsonResponse(w, map[string]string{"status": "restarted", "name": name})
}

// ============ System Resources ============

func (s *Server) handleSystemResources(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	result := map[string]any{
		"cpus":            runtime.NumCPU(),
		"goroutines":      runtime.NumGoroutine(),
		"memory_alloc_mb": float64(memStats.Alloc) / 1024 / 1024,
		"memory_sys_mb":   float64(memStats.Sys) / 1024 / 1024,
		"gc_cycles":       memStats.NumGC,
	}

	// Disk usage of web root
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	if webRoot != "" {
		if du, err := filemanager.DiskUsage(webRoot); err == nil {
			result["disk_used_bytes"] = du
			result["disk_used_mb"] = float64(du) / 1024 / 1024
		}
	}

	jsonResponse(w, result)
}

// ============ Security Stats ============

// SetSecurityStats sets the security stats tracker for the API.
func (s *Server) SetSecurityStats(st *middleware.SecurityStats) { s.securityStats = st }

func (s *Server) handleSecurityStats(w http.ResponseWriter, r *http.Request) {
	if s.securityStats == nil {
		jsonResponse(w, map[string]any{
			"waf_blocked": 0, "bot_blocked": 0, "rate_blocked": 0,
			"hotlink_blocked": 0, "total_blocked": 0,
		})
		return
	}
	jsonResponse(w, s.securityStats.Snapshot())
}

func (s *Server) handleSecurityBlocked(w http.ResponseWriter, r *http.Request) {
	if s.securityStats == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, s.securityStats.RecentBlocked())
}

// ── Doctor ─────────────────────────────────────────────────

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	report := doctorRun(doctor.Options{
		ConfigPath: s.configPath,
		WebRoot:    webRoot,
		AutoFix:    false,
	})
	jsonResponse(w, report)
}

func (s *Server) handleDoctorFix(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	report := doctorRun(doctor.Options{
		ConfigPath: s.configPath,
		WebRoot:    webRoot,
		AutoFix:    true,
	})

	fixed := 0
	for _, c := range report.Checks {
		if c.Status == "fixed" {
			fixed++
		}
	}
	s.recordAuditR(r, "doctor.fix", fmt.Sprintf("%d issues fixed", fixed), true)
	jsonResponse(w, report)
}

// ============ Package Installer ============

// PackageInfo describes an installable system package.
type PackageInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
	Category    string `json:"category"`
	Required    bool   `json:"required"`          // true if UWAS needs this
	UsedBy      string `json:"used_by,omitempty"` // what uses it: "WordPress", "Image Optimization", etc
	Warning     string `json:"warning,omitempty"` // uninstall warning
	CanRemove   bool   `json:"can_remove"`        // false if critical dependency
}

type knownPkg struct {
	id, name, description, category string
	required                        bool
	usedBy                          string
	warning                         string // shown before uninstall
	canRemove                       bool
	binaries                        []string
	aptPkgs                         []string
	aptRemove                       []string // packages to remove (may differ from install)
}

var knownPackages = []knownPkg{
	// ── Core (UWAS needs these) ──
	{"mariadb", "MariaDB", "Database for WordPress and web apps", "Database",
		true, "WordPress, Database page", "ALL databases will be destroyed! Back up first.", true,
		[]string{"mariadbd", "mysqld"}, []string{"mariadb-server", "mariadb-client"}, []string{"mariadb-server", "mariadb-client"}},

	// ── PHP (managed separately via PHP page) ──

	// ── Docker ──
	{"docker", "Docker", "Container runtime for dockerized databases and software library apps", "Infrastructure",
		false, "Database page, Software Library", "All Docker containers will remain, only Docker engine removed.", true,
		[]string{"docker"}, []string{"docker.io"}, []string{"docker.io"}},
	{"docker-compose", "Docker Compose", "Compose plugin for one-click Software Library apps", "Infrastructure",
		true, "Software Library", "Dockerized software will stop working without Compose.", false,
		nil, []string{"docker.io", "docker-compose-plugin"}, nil},

	// ── Image Optimization ──
	{"webp", "WebP Tools", "Convert images to WebP (smaller, faster loading)", "Performance",
		false, "Image Optimization (per-domain)", "", true,
		[]string{"cwebp"}, []string{"webp"}, []string{"webp"}},
	{"avif", "AVIF Tools", "Convert images to AVIF (next-gen format)", "Performance",
		false, "Image Optimization (per-domain)", "", true,
		[]string{"avifenc"}, []string{"libavif-bin"}, []string{"libavif-bin"}},

	// ── Cache backends ──
	{"redis", "Redis", "In-memory cache and queue backend for apps and UWAS cache", "Performance",
		false, "Apps, cache layer", "Apps using Redis queues/cache will fail until reinstalled.", true,
		[]string{"redis-server", "redis-cli"}, []string{"redis-server", "redis-tools"}, []string{"redis-server", "redis-tools"}},
	{"memcached", "Memcached", "Lightweight in-memory object cache for PHP and web apps", "Performance",
		false, "WordPress object cache, PHP apps", "Apps using Memcached object cache will fail until reinstalled.", true,
		[]string{"memcached"}, []string{"memcached", "libmemcached-tools"}, []string{"memcached", "libmemcached-tools"}},

	// ── Security ──
	{"ufw", "UFW Firewall", "Manage firewall rules from dashboard", "Security",
		true, "Firewall page", "All firewall rules will be removed!", true,
		[]string{"ufw"}, []string{"ufw"}, []string{"ufw"}},
	{"fail2ban", "Fail2Ban", "Auto-block brute-force attacks on SSH/HTTP", "Security",
		false, "SSH + admin panel protection", "", true,
		[]string{"fail2ban-client"}, []string{"fail2ban"}, []string{"fail2ban"}},

	// ── WordPress ──
	{"wp-cli", "WP-CLI", "Manage WordPress from dashboard (updates, plugins, themes)", "WordPress",
		false, "WordPress Sites page (plugin/theme management)", "", true,
		[]string{"wp"}, nil, nil},

	// ── Email ──
	{"postfix", "Postfix", "Send emails from your server (SMTP)", "Email",
		false, "WordPress email sending, contact forms", "Server will not be able to send emails!", true,
		[]string{"postfix"}, []string{"postfix"}, []string{"postfix"}},

	// ── Utilities (required by UWAS internals) ──
	{"curl", "cURL", "HTTP client (used for ACME, health checks, WP-CLI)", "Required",
		true, "SSL certificates, health monitoring", "", false,
		[]string{"curl"}, []string{"curl"}, nil},
	{"unzip", "Unzip", "Extract archives (used for WordPress install)", "Required",
		true, "WordPress installer", "", false,
		[]string{"unzip"}, []string{"unzip"}, nil},

	// ── App Runtimes (for standalone apps) ──
	{"nodejs", "Node.js + npm", "JavaScript runtime for Node.js apps (Express, Next.js, etc.)", "Runtime",
		false, "Apps page (runtime=node)", "Running Node.js apps will fail until reinstalled.", true,
		[]string{"node"}, []string{"nodejs", "npm"}, []string{"nodejs", "npm"}},
	{"python3", "Python 3 + pip", "Python interpreter + pip for Python web apps (Flask, Django, FastAPI)", "Runtime",
		false, "Apps page (runtime=python)", "Running Python apps will fail until reinstalled.", true,
		[]string{"python3"}, []string{"python3", "python3-pip", "python3-venv"}, []string{"python3-pip", "python3-venv"}},
	{"ruby", "Ruby", "Ruby interpreter for Ruby web apps (Rails, Sinatra)", "Runtime",
		false, "Apps page (runtime=ruby)", "Running Ruby apps will fail until reinstalled.", true,
		[]string{"ruby"}, []string{"ruby-full"}, []string{"ruby-full"}},
	{"golang", "Go", "Go toolchain for building Go web apps", "Runtime",
		false, "Apps page (runtime=go)", "Building Go apps will fail until reinstalled.", true,
		[]string{"go"}, []string{"golang-go"}, []string{"golang-go"}},
}

func (s *Server) handlePackageList(w http.ResponseWriter, r *http.Request) {
	pkgs := make([]PackageInfo, 0, len(knownPackages))
	for _, kp := range knownPackages {
		pi := PackageInfo{
			ID:          kp.id,
			Name:        kp.name,
			Description: kp.description,
			Category:    kp.category,
			Required:    kp.required,
			UsedBy:      kp.usedBy,
			Warning:     kp.warning,
			CanRemove:   kp.canRemove,
		}
		if kp.id == "docker-compose" {
			pi.Installed, pi.Version = detectDockerComposePackage()
		} else {
			for _, bin := range kp.binaries {
				if p, err := exec.LookPath(bin); err == nil {
					pi.Installed = true
					if out, err := newSystemExecCommand(p, "--version").CombinedOutput(); err == nil {
						pi.Version = packageVersionLine(out)
					}
					break
				}
			}
		}
		pkgs = append(pkgs, pi)
	}
	limit, offset := parsePagination(r)
	pkgs, total := paginateSlice(pkgs, limit, offset)
	jsonResponse(w, map[string]any{"items": pkgs, "total": total, "limit": limit, "offset": offset})
}

func detectDockerComposePackage() (bool, string) {
	if out, err := newSystemExecCommand("docker", "compose", "version").CombinedOutput(); err == nil {
		return true, packageVersionLine(out)
	}
	if out, err := newSystemExecCommand("docker-compose", "--version").CombinedOutput(); err == nil {
		return true, packageVersionLine(out)
	}
	return false, ""
}

func packageVersionLine(out []byte) string {
	lines := strings.SplitN(string(out), "\n", 2)
	if len(lines) == 0 {
		return ""
	}
	v := strings.TrimSpace(lines[0])
	if len(v) > 60 {
		v = v[:60]
	}
	return v
}

// Package installation is managed by the global task queue (install.Queue).

func findPkg(id string) *knownPkg {
	for i := range knownPackages {
		if knownPackages[i].id == id {
			return &knownPackages[i]
		}
	}
	return nil
}

func (s *Server) handlePackageInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		ID     string `json:"id"`
		Action string `json:"action"` // "install" (default) or "remove"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Action == "" {
		req.Action = "install"
	}

	pkg := findPkg(req.ID)
	if pkg == nil {
		jsonError(w, "unknown package: "+req.ID, http.StatusBadRequest)
		return
	}

	// Uninstall validation
	if req.Action == "remove" {
		if !pkg.canRemove {
			jsonError(w, pkg.name+" is required by UWAS and cannot be removed", http.StatusForbidden)
			return
		}
		if len(pkg.aptRemove) == 0 && pkg.id != "wp-cli" {
			jsonError(w, "no removal method for "+pkg.name, http.StatusBadRequest)
			return
		}
	}

	// Check if any install task is already running
	if active := s.taskMgr.Active(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}

	action := req.Action
	s.recordAuditR(r, "package."+action, pkg.name, true)

	pkgName := pkg.name
	pkgID := pkg.id
	aptPkgs := pkg.aptPkgs
	aptRemove := pkg.aptRemove

	task := s.taskMgr.Submit("package", pkgName, action, func(appendOutput func(string)) error {
		var cmd *exec.Cmd

		if action == "remove" {
			if pkgID == "wp-cli" {
				cmd = newSystemExecCommand("rm", "-f", "/usr/local/bin/wp")
			} else {
				newSystemExecCommand("systemctl", "stop", pkgID).Run()
				args := append([]string{"remove", "-y", "--purge"}, aptRemove...)
				cmd = newSystemExecCommand("apt", args...)
			}
		} else {
			if pkgID == "wp-cli" {
				cmd = newSystemExecCommand("bash", "-c", "curl -fsSL -o /usr/local/bin/wp https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x /usr/local/bin/wp")
			} else if pkgID == "nodejs" {
				// Distro nodejs is typically too old to run modern apps.
				// Use NodeSource LTS setup so Apps page Node.js sites work out of the box.
				cmd = newSystemExecCommand("bash", "-c", "curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - && apt install -y nodejs")
			} else if pkgID == "docker-compose" {
				cmd = newSystemExecCommand("bash", "-c", strings.Join([]string{
					"apt-get update",
					"(apt-get install -y docker.io docker-compose-plugin || apt-get install -y docker.io docker-compose)",
					"(systemctl enable --now docker >/dev/null 2>&1 || service docker start >/dev/null 2>&1 || true)",
				}, " && "))
			} else if len(aptPkgs) > 0 {
				args := append([]string{"install", "-y"}, aptPkgs...)
				cmd = newSystemExecCommand("apt", args...)
			} else {
				return fmt.Errorf("no install method for %s", pkgName)
			}
		}

		cmd.Env = append(os.Environ(),
			"DEBIAN_FRONTEND=noninteractive",
			"NEEDRESTART_MODE=a",
			"APT_LISTCHANGES_FRONTEND=none",
			"DEBIAN_PRIORITY=critical",
		)
		out, err := cmd.CombinedOutput()
		appendOutput(string(out))
		if err != nil {
			s.logger.Error("package "+action+" failed", "package", pkgName, "error", err)
			return err
		}
		s.logger.Info("package "+action+" complete", "package", pkgName)
		return nil
	})

	jsonResponse(w, map[string]string{"status": action + "ing", "package": pkgName, "task_id": task.ID})
}
