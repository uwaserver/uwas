// Package doctor diagnoses and auto-fixes common UWAS issues.
package doctor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Status represents the result of a single check.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusFixed Status = "fixed"
)

// Check is a single diagnostic result.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`     // what was auto-fixed
	HowTo   string `json:"how_to,omitempty"`  // manual fix instructions
}

// Report is the full doctor report.
type Report struct {
	Checks  []Check `json:"checks"`
	Summary string  `json:"summary"`
}

// Options configures the doctor run.
type Options struct {
	ConfigPath string
	WebRoot    string
	AutoFix    bool // attempt to fix issues automatically
}

// Run performs all diagnostic checks and optionally fixes issues.
func Run(opts Options) *Report {
	r := &Report{}

	// System checks
	r.add(checkOS())
	r.add(checkPorts())
	r.add(checkPHPFPM(opts.AutoFix))
	r.add(checkPHPModules())
	r.add(checkMySQL(opts.AutoFix))
	r.add(checkWebRoot(opts.WebRoot, opts.AutoFix))
	r.add(checkConfigFile(opts.ConfigPath))
	r.add(checkDomainsDir(opts.ConfigPath))
	r.add(checkSSLCerts(opts.ConfigPath))
	r.add(checkFirewall())
	r.add(checkDiskSpace())
	r.add(checkMemory())
	r.add(checkOpenFiles())
	r.add(checkTimeSync())
	r.add(checkDNS())

	// Summary
	ok, warn, fail, fixed := 0, 0, 0, 0
	for _, c := range r.Checks {
		switch c.Status {
		case StatusOK:
			ok++
		case StatusWarn:
			warn++
		case StatusFail:
			fail++
		case StatusFixed:
			fixed++
		}
	}
	r.Summary = fmt.Sprintf("%d ok, %d warnings, %d failures, %d auto-fixed", ok, warn, fail, fixed)
	return r
}

func (r *Report) add(c Check) {
	r.Checks = append(r.Checks, c)
}

// ── Individual checks ──────────────────────────────────────

func checkOS() Check {
	if runtime.GOOS != "linux" {
		return Check{Name: "Operating System", Status: StatusWarn, Message: fmt.Sprintf("%s/%s — production should be Linux", runtime.GOOS, runtime.GOARCH)}
	}
	return Check{Name: "Operating System", Status: StatusOK, Message: fmt.Sprintf("Linux %s", runtime.GOARCH)}
}

func checkPorts() Check {
	issues := []string{}
	for _, port := range []string{":80", ":443"} {
		ln, err := net.Listen("tcp", port)
		if err != nil {
			// Port in use — check if it's UWAS
			if strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "bind") {
				issues = append(issues, fmt.Sprintf("port %s in use", port))
			}
		} else {
			ln.Close()
		}
	}
	if len(issues) > 0 {
		return Check{Name: "Ports 80/443", Status: StatusWarn, Message: strings.Join(issues, "; "), HowTo: "UWAS or another server is already listening (this is normal if UWAS is running)"}
	}
	return Check{Name: "Ports 80/443", Status: StatusOK, Message: "Available"}
}

func checkPHPFPM(autoFix bool) Check {
	// Check for php-fpm socket
	sockets := []string{
		"/run/php/php8.4-fpm.sock",
		"/run/php/php8.3-fpm.sock",
		"/run/php/php8.2-fpm.sock",
		"/run/php/php8.1-fpm.sock",
		"/run/php/php-fpm.sock",
	}
	for _, sock := range sockets {
		if _, err := os.Stat(sock); err == nil {
			conn, err := net.DialTimeout("unix", sock, 2*time.Second)
			if err == nil {
				conn.Close()
				return Check{Name: "PHP-FPM", Status: StatusOK, Message: fmt.Sprintf("Running at %s", sock)}
			}
			// Socket exists but not listening
			if autoFix {
				ver := extractPHPVersion(sock)
				if ver != "" {
					exec.Command("systemctl", "start", fmt.Sprintf("php%s-fpm", ver)).Run()
					// Re-check
					conn2, err2 := net.DialTimeout("unix", sock, 2*time.Second)
					if err2 == nil {
						conn2.Close()
						return Check{Name: "PHP-FPM", Status: StatusFixed, Message: fmt.Sprintf("Started php%s-fpm", ver), Fix: fmt.Sprintf("systemctl start php%s-fpm", ver)}
					}
				}
			}
			return Check{Name: "PHP-FPM", Status: StatusFail, Message: fmt.Sprintf("Socket %s exists but not listening", sock), HowTo: "sudo systemctl start php8.3-fpm"}
		}
	}

	// No socket — check if php-fpm is installed
	if _, err := exec.LookPath("php-fpm8.3"); err == nil {
		if autoFix {
			exec.Command("systemctl", "start", "php8.3-fpm").Run()
			time.Sleep(time.Second)
			for _, sock := range sockets {
				conn, err := net.DialTimeout("unix", sock, 2*time.Second)
				if err == nil {
					conn.Close()
					return Check{Name: "PHP-FPM", Status: StatusFixed, Message: "Started php8.3-fpm", Fix: "systemctl start php8.3-fpm"}
				}
			}
		}
		return Check{Name: "PHP-FPM", Status: StatusFail, Message: "php-fpm installed but not running", HowTo: "sudo systemctl start php8.3-fpm && sudo systemctl enable php8.3-fpm"}
	}

	// Check php-cgi as fallback
	if _, err := exec.LookPath("php-cgi8.3"); err == nil {
		return Check{Name: "PHP-FPM", Status: StatusWarn, Message: "Only php-cgi found (slow, single-threaded)", HowTo: "sudo apt install php8.3-fpm for production performance"}
	}

	return Check{Name: "PHP-FPM", Status: StatusFail, Message: "No PHP-FPM or PHP-CGI found", HowTo: "sudo apt install php8.3-fpm php8.3-mysql php8.3-curl php8.3-gd php8.3-mbstring php8.3-xml php8.3-zip"}
}

func checkPHPModules() Check {
	out, err := exec.Command("php", "-m").Output()
	if err != nil {
		return Check{Name: "PHP Modules", Status: StatusWarn, Message: "Could not check PHP modules"}
	}
	mods := string(out)
	required := []string{"mysqli", "curl", "gd", "mbstring", "xml", "zip"}
	missing := []string{}
	for _, mod := range required {
		if !strings.Contains(strings.ToLower(mods), strings.ToLower(mod)) {
			missing = append(missing, mod)
		}
	}
	if len(missing) > 0 {
		return Check{Name: "PHP Modules", Status: StatusWarn, Message: fmt.Sprintf("Missing: %s", strings.Join(missing, ", ")), HowTo: fmt.Sprintf("sudo apt install %s", modulesToPackages(missing))}
	}
	return Check{Name: "PHP Modules", Status: StatusOK, Message: fmt.Sprintf("%d required modules present", len(required))}
}

func checkMySQL(autoFix bool) Check {
	// Check if MySQL/MariaDB is running
	for _, svc := range []string{"mariadb", "mysql"} {
		out, _ := exec.Command("systemctl", "is-active", svc).Output()
		if strings.TrimSpace(string(out)) == "active" {
			return Check{Name: "MySQL/MariaDB", Status: StatusOK, Message: fmt.Sprintf("Running (%s)", svc)}
		}
	}

	// Check if installed
	installed := false
	for _, bin := range []string{"mariadb", "mysql", "mariadbd", "mysqld"} {
		if _, err := exec.LookPath(bin); err == nil {
			installed = true
			break
		}
	}

	if !installed {
		if autoFix {
			// Install MariaDB
			if _, err := exec.LookPath("apt"); err == nil {
				exec.Command("apt", "install", "-y", "mariadb-server", "mariadb-client").Run()
				exec.Command("systemctl", "start", "mariadb").Run()
				exec.Command("systemctl", "enable", "mariadb").Run()
				out, _ := exec.Command("systemctl", "is-active", "mariadb").Output()
				if strings.TrimSpace(string(out)) == "active" {
					return Check{Name: "MySQL/MariaDB", Status: StatusFixed, Message: "Installed and started MariaDB", Fix: "apt install mariadb-server && systemctl start mariadb"}
				}
			}
		}
		return Check{Name: "MySQL/MariaDB", Status: StatusWarn, Message: "Not installed (needed for WordPress)", HowTo: "sudo apt install mariadb-server"}
	}

	// Installed but not running — diagnose why
	issues := []string{}

	// Check data directory
	if _, err := os.Stat("/var/lib/mysql"); os.IsNotExist(err) {
		issues = append(issues, "data directory /var/lib/mysql missing")
	}

	// Check socket directories
	for _, dir := range []string{"/run/mysqld", "/var/run/mysqld"} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			issues = append(issues, fmt.Sprintf("socket directory %s missing", dir))
		}
	}

	// Check dpkg state
	if _, err := exec.LookPath("dpkg"); err == nil {
		out, _ := exec.Command("dpkg", "--audit").Output()
		if len(strings.TrimSpace(string(out))) > 0 {
			issues = append(issues, "dpkg has broken packages")
		}
	}

	if !autoFix {
		msg := "Installed but not running"
		if len(issues) > 0 {
			msg += ": " + strings.Join(issues, ", ")
		}
		return Check{Name: "MySQL/MariaDB", Status: StatusFail, Message: msg, HowTo: "Run Doctor with auto-fix, or call POST /api/v1/database/repair"}
	}

	// AUTO-FIX: full repair sequence
	var fixLog []string

	// 1. Kill stuck processes
	exec.Command("pkill", "-9", "mysqld").Run()
	exec.Command("pkill", "-9", "mariadbd").Run()

	// 2. Fix dpkg/apt state
	if _, err := exec.LookPath("dpkg"); err == nil {
		exec.Command("dpkg", "--configure", "-a").Run()
		exec.Command("apt", "--fix-broken", "install", "-y").Run()
		fixLog = append(fixLog, "dpkg/apt fixed")
	}

	// 3. Create directories
	for _, dir := range []string{"/var/lib/mysql", "/run/mysqld", "/var/run/mysqld", "/var/log/mysql"} {
		os.MkdirAll(dir, 0755)
		exec.Command("chown", "mysql:mysql", dir).Run()
	}
	fixLog = append(fixLog, "created data/socket/log dirs")

	// 4. Init database if data dir was missing
	for _, bin := range []string{"mariadb-install-db", "mysql_install_db"} {
		if path, err := exec.LookPath(bin); err == nil {
			exec.Command(path, "--user=mysql", "--datadir=/var/lib/mysql").Run()
			fixLog = append(fixLog, "initialized database")
			break
		}
	}

	// 5. Start service
	for _, svc := range []string{"mariadb", "mysql"} {
		if err := exec.Command("systemctl", "start", svc).Run(); err == nil {
			exec.Command("systemctl", "enable", svc).Run()
			fixLog = append(fixLog, "started "+svc)
			return Check{Name: "MySQL/MariaDB", Status: StatusFixed, Message: "Fully repaired and started", Fix: strings.Join(fixLog, " → ")}
		}
	}

	return Check{Name: "MySQL/MariaDB", Status: StatusFail, Message: "Repair attempted but service still won't start: " + strings.Join(fixLog, ", "), HowTo: "Check journalctl -u mariadb, or use POST /api/v1/database/force-uninstall then POST /api/v1/database/install"}
}

func checkWebRoot(webRoot string, autoFix bool) Check {
	if webRoot == "" {
		webRoot = "/var/www"
	}
	if _, err := os.Stat(webRoot); os.IsNotExist(err) {
		if autoFix {
			os.MkdirAll(webRoot, 0755)
			exec.Command("chown", "www-data:www-data", webRoot).Run()
			return Check{Name: "Web Root", Status: StatusFixed, Message: fmt.Sprintf("Created %s", webRoot), Fix: fmt.Sprintf("mkdir -p %s && chown www-data:www-data %s", webRoot, webRoot)}
		}
		return Check{Name: "Web Root", Status: StatusFail, Message: fmt.Sprintf("%s does not exist", webRoot), HowTo: fmt.Sprintf("sudo mkdir -p %s && sudo chown www-data:www-data %s", webRoot, webRoot)}
	}
	return Check{Name: "Web Root", Status: StatusOK, Message: webRoot}
}

func checkConfigFile(configPath string) Check {
	if configPath == "" {
		// Try common paths
		for _, p := range []string{"/etc/uwas/uwas.yaml", "/opt/uwas/uwas.yaml", "./uwas.yaml"} {
			if _, err := os.Stat(p); err == nil {
				return Check{Name: "Config File", Status: StatusOK, Message: p}
			}
		}
		return Check{Name: "Config File", Status: StatusWarn, Message: "No config file found — run uwas serve to create one"}
	}
	if _, err := os.Stat(configPath); err != nil {
		return Check{Name: "Config File", Status: StatusFail, Message: fmt.Sprintf("%s not found", configPath)}
	}
	return Check{Name: "Config File", Status: StatusOK, Message: configPath}
}

func checkDomainsDir(configPath string) Check {
	dir := "domains.d"
	if configPath != "" {
		dir = filepath.Join(filepath.Dir(configPath), "domains.d")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Check{Name: "Domains Directory", Status: StatusWarn, Message: fmt.Sprintf("%s not found or empty", dir)}
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			count++
		}
	}
	return Check{Name: "Domains Directory", Status: StatusOK, Message: fmt.Sprintf("%d domain(s) in %s", count, dir)}
}

func checkSSLCerts(configPath string) Check {
	dir := "/etc/uwas/certs"
	if configPath != "" {
		dir = filepath.Join(filepath.Dir(configPath), "certs")
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return Check{Name: "SSL Certificates", Status: StatusWarn, Message: "No certs directory — SSL will be requested on first domain add"}
	}
	entries, _ := os.ReadDir(dir)
	return Check{Name: "SSL Certificates", Status: StatusOK, Message: fmt.Sprintf("%d certificate(s)", len(entries)/2)}
}

func checkFirewall() Check {
	out, err := exec.Command("ufw", "status").Output()
	if err != nil {
		return Check{Name: "Firewall", Status: StatusWarn, Message: "ufw not available"}
	}
	status := string(out)
	if strings.Contains(status, "inactive") {
		return Check{Name: "Firewall", Status: StatusWarn, Message: "ufw is inactive", HowTo: "sudo ufw allow 80,443/tcp && sudo ufw enable"}
	}
	if !strings.Contains(status, "80") || !strings.Contains(status, "443") {
		return Check{Name: "Firewall", Status: StatusWarn, Message: "Port 80/443 may not be allowed in firewall", HowTo: "sudo ufw allow 80,443/tcp"}
	}
	return Check{Name: "Firewall", Status: StatusOK, Message: "Active, ports 80/443 allowed"}
}

func checkDiskSpace() Check {
	// Simple check using df
	out, err := exec.Command("df", "-h", "/").Output()
	if err != nil {
		return Check{Name: "Disk Space", Status: StatusWarn, Message: "Could not check disk space"}
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 5 {
			usage := fields[4] // e.g., "42%"
			return Check{Name: "Disk Space", Status: StatusOK, Message: fmt.Sprintf("%s used (%s available)", usage, fields[3])}
		}
	}
	return Check{Name: "Disk Space", Status: StatusOK, Message: "OK"}
}

func checkDNS() Check {
	// Check if we can resolve external domains
	_, err := net.LookupHost("acme-v02.api.letsencrypt.org")
	if err != nil {
		return Check{Name: "DNS Resolution", Status: StatusFail, Message: "Cannot resolve Let's Encrypt API — SSL certificates will fail", HowTo: "Check /etc/resolv.conf and DNS settings"}
	}
	return Check{Name: "DNS Resolution", Status: StatusOK, Message: "Working"}
}

// ── Helpers ──────────────────────────────────────────

func checkMemory() Check {
	out, err := exec.Command("free", "-m").Output()
	if err != nil {
		return Check{Name: "Memory", Status: StatusWarn, Message: "Could not check memory"}
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 4 {
			return Check{Name: "Memory", Status: StatusOK, Message: fmt.Sprintf("Total: %sMB, Used: %sMB, Available: %sMB", fields[1], fields[2], fields[len(fields)-1])}
		}
	}
	return Check{Name: "Memory", Status: StatusOK, Message: "OK"}
}

func checkOpenFiles() Check {
	out, err := exec.Command("ulimit", "-n").Output()
	if err != nil {
		// ulimit is a shell builtin, try reading from proc
		data, err2 := os.ReadFile("/proc/sys/fs/file-max")
		if err2 != nil {
			return Check{Name: "Open Files Limit", Status: StatusWarn, Message: "Could not check"}
		}
		max := strings.TrimSpace(string(data))
		return Check{Name: "Open Files Limit", Status: StatusOK, Message: fmt.Sprintf("System max: %s", max)}
	}
	limit := strings.TrimSpace(string(out))
	return Check{Name: "Open Files Limit", Status: StatusOK, Message: fmt.Sprintf("ulimit -n: %s", limit)}
}

func checkTimeSync() Check {
	out, err := exec.Command("timedatectl", "show", "--property=NTPSynchronized", "--value").Output()
	if err != nil {
		return Check{Name: "Time Sync", Status: StatusWarn, Message: "Could not check NTP status"}
	}
	synced := strings.TrimSpace(string(out))
	if synced == "yes" {
		return Check{Name: "Time Sync", Status: StatusOK, Message: "NTP synchronized"}
	}
	return Check{Name: "Time Sync", Status: StatusWarn, Message: "NTP not synchronized — SSL certificates may fail", HowTo: "sudo timedatectl set-ntp true"}
}

func extractPHPVersion(socketPath string) string {
	// /run/php/php8.3-fpm.sock → 8.3
	base := filepath.Base(socketPath)
	base = strings.TrimPrefix(base, "php")
	base = strings.TrimSuffix(base, "-fpm.sock")
	if len(base) >= 3 && base[1] == '.' {
		return base
	}
	return ""
}

func modulesToPackages(modules []string) string {
	pkgs := make([]string, len(modules))
	for i, m := range modules {
		pkgs[i] = "php-" + strings.ToLower(m)
	}
	return strings.Join(pkgs, " ")
}
