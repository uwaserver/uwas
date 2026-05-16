package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Hooks for testing install.go functions.
var (
	installExecCommand  = exec.Command
	installRuntimeGOOS  = runtime.GOOS
	installOsGetuid     = os.Getuid
	installOsExecutable = os.Executable
	installOsWriteFile  = os.WriteFile
	installOsReadFile   = os.ReadFile
	installOsRemove     = os.Remove
	installOsSymlink    = os.Symlink
	installOsStat       = os.Stat
	installOsMkdirAll   = os.MkdirAll
	// installStdin is the source for the interactive [Y/n] prompt; replaceable in tests.
	installStdin = func() *os.File { return os.Stdin }
	// installIsTTY reports whether the install prompt should be shown. Wrapped
	// in a var so tests can force non-interactive behavior regardless of how
	// the test runner attaches stdin (Windows cmd terminals report stdin as a
	// character device even when go test pipes /dev/null in).
	installIsTTY = func() bool {
		fi, err := installStdin().Stat()
		if err != nil {
			return false
		}
		return fi.Mode()&os.ModeCharDevice != 0
	}
)

// InstallCmd installs UWAS as a system service.
type InstallCmd struct{}

func (c *InstallCmd) Name() string        { return "install" }
func (c *InstallCmd) Description() string { return "Install UWAS as system service" }
func (c *InstallCmd) Run(args []string) error {
	return installUWAS(args)
}

// DoctorCmd runs system diagnostics.
type DoctorCmd struct{}

func (c *DoctorCmd) Name() string        { return "doctor" }
func (c *DoctorCmd) Description() string { return "Diagnose and fix system issues" }
func (c *DoctorCmd) Run(args []string) error {
	return DoctorCommand(args)
}

// installUWAS is the system-service installer.
//
// Layout it produces:
//
//	/usr/local/bin/uwas             — binary (copy of the running executable)
//	/usr/bin/uwas                   — convenience symlink → /usr/local/bin/uwas
//	/etc/uwas/uwas.yaml             — seeded if missing; never overwritten
//	/etc/uwas/.env                  — UWAS_ADMIN_KEY for CLI; never overwritten
//	/etc/uwas/domains.d/            — empty per-domain include dir
//	/etc/systemd/system/uwas.service
//
// Flags:
//
//	--no-start    install + enable, but don't start the service
//	--no-config   skip /etc/uwas/uwas.yaml seeding (operators with custom configs)
//	--yes -y      non-interactive — assume "yes" on every prompt
func installUWAS(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	noStart := fs.Bool("no-start", false, "install but do not start the service")
	noConfig := fs.Bool("no-config", false, "do not seed /etc/uwas/uwas.yaml")
	yes := fs.Bool("yes", false, "non-interactive — assume yes on every prompt")
	fs.BoolVar(yes, "y", false, "alias for --yes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if installRuntimeGOOS != "linux" {
		return fmt.Errorf("install command is only supported on Linux")
	}
	if installOsGetuid() != 0 {
		return fmt.Errorf("install requires root — run with sudo")
	}

	binPath := "/usr/local/bin/uwas"
	servicePath := "/etc/systemd/system/uwas.service"
	configDir := "/etc/uwas"

	fmt.Println("Installing UWAS...")

	// 1. Copy current binary to /usr/local/bin/uwas
	self, err := installOsExecutable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	self, _ = filepath.EvalSymlinks(self)

	if self != binPath {
		data, err := installOsReadFile(self)
		if err != nil {
			return fmt.Errorf("read binary: %w", err)
		}
		if err := installOsWriteFile(binPath, data, 0755); err != nil {
			return fmt.Errorf("write %s: %w", binPath, err)
		}
		fmt.Printf("  ✓ Binary installed: %s\n", binPath)
	} else {
		fmt.Printf("  ✓ Binary already at %s\n", binPath)
	}

	// 2. Create config + per-domain include directories.
	if err := installOsMkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", configDir, err)
	}
	domainsDir := filepath.Join(configDir, "domains.d")
	if err := installOsMkdirAll(domainsDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", domainsDir, err)
	}
	fmt.Printf("  ✓ Config directory: %s\n", configDir)

	// 2.5. Seed /etc/uwas/uwas.yaml + .env if missing (skipped with --no-config).
	cfgPath := filepath.Join(configDir, "uwas.yaml")
	envPath := filepath.Join(configDir, ".env")
	if !*noConfig {
		if _, statErr := installOsStat(cfgPath); os.IsNotExist(statErr) {
			// System install defaults: bind admin to loopback only, /var/www web root,
			// /var/lib/uwas for cert/cache/backup storage. Operators who want public
			// admin access can flip global.admin.listen by hand or via dashboard.
			apiKey := generateAPIKey()
			pinCode := generatePinCode()
			content := generateDefaultConfig(
				"80", "9443", "127.0.0.1",
				apiKey, pinCode,
				"/var/lib/uwas",
				"/var/www",
				"",
			)
			if err := installOsWriteFile(cfgPath, []byte(content), 0600); err != nil {
				return fmt.Errorf("write %s: %w", cfgPath, err)
			}
			envContent := fmt.Sprintf("UWAS_ADMIN_KEY=%s\nUWAS_PURGE_KEY=%s\n", apiKey, generateAPIKey())
			_ = installOsWriteFile(envPath, []byte(envContent), 0600)
			fmt.Printf("  ✓ Baseline config: %s\n", cfgPath)
		} else {
			fmt.Printf("  ✓ Config already exists: %s (untouched)\n", cfgPath)
		}
	}

	// 3. Create runtime-storage dirs the systemd service expects.
	for _, d := range []string{"/var/lib/uwas", "/var/cache/uwas", "/var/log/uwas", "/var/www"} {
		if err := installOsMkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}

	// 4. Create systemd service. Reads /etc/uwas/uwas.yaml so the seeded baseline
	// above is what the service serves on first boot.
	service := `[Unit]
Description=UWAS — Unified Web Application Server
Documentation=https://github.com/uwaserver/uwas
After=network.target php8.3-fpm.service mariadb.service
Wants=php8.3-fpm.service

[Service]
Type=simple
ExecStart=/usr/local/bin/uwas serve -c /etc/uwas/uwas.yaml
ExecStop=/usr/local/bin/uwas stop
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5
User=root
WorkingDirectory=/etc/uwas
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`
	if err := installOsWriteFile(servicePath, []byte(service), 0644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	fmt.Printf("  ✓ Systemd service: %s\n", servicePath)

	// 5. Reload systemd, enable on-boot.
	if err := installExecCommand("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := installExecCommand("systemctl", "enable", "uwas").Run(); err != nil {
		return fmt.Errorf("systemctl enable uwas: %w", err)
	}
	fmt.Println("  ✓ Service enabled (starts on boot)")

	// 5b. Stop any old running instance before we (re)start. This matters on
	// upgrade: install.sh replaced the binary on disk while the old uwas was
	// still running. If we just `systemctl start` now, the new binary's
	// already-instance check would fire, ExecStart would exit 0, systemd
	// would mark the service deactivated, ExecStop would run, and the OLD
	// uwas would be killed — leaving nothing running. Stop first, then start
	// a clean process.
	_ = installExecCommand("systemctl", "stop", "uwas").Run()

	// 6. Convenience symlink at /usr/bin/uwas.
	if _, err := installOsStat("/usr/bin/uwas"); err == nil {
		// Already exists.
	} else if os.IsNotExist(err) {
		if err := installOsSymlink(binPath, "/usr/bin/uwas"); err != nil {
			return fmt.Errorf("create symlink /usr/bin/uwas: %w", err)
		}
	} else {
		return fmt.Errorf("stat /usr/bin/uwas: %w", err)
	}

	// 7. Start now? Default yes; interactive prompt only when stdin is a TTY
	// AND --yes wasn't passed. Non-interactive runs (curl|sh, CI) auto-start.
	doStart := !*noStart
	if doStart && !*yes && installIsTTY() {
		fmt.Println()
		fmt.Print("Start UWAS now? [Y/n] ")
		reader := bufio.NewReader(installStdin())
		line, _ := reader.ReadString('\n')
		resp := strings.ToLower(strings.TrimSpace(line))
		if resp != "" && resp != "y" && resp != "yes" {
			doStart = false
		}
	}

	if doStart {
		if err := installExecCommand("systemctl", "start", "uwas").Run(); err != nil {
			fmt.Printf("  ⚠ systemctl start uwas failed: %v\n", err)
			fmt.Println("    Inspect logs: journalctl -u uwas -e")
		} else {
			fmt.Println("  ✓ Service started")
		}
	}

	// 8. Final summary — pull the live api_key / pin_code / admin listen from the
	// actual config so this output is correct whether we just seeded a fresh
	// /etc/uwas/uwas.yaml or left an operator's existing one untouched.
	fmt.Println()
	fmt.Println("Installation complete.")
	fmt.Println()
	fmt.Println("━━━ UWAS is ready ━━━")
	fmt.Println()
	creds := extractCredsFromConfig(cfgPath)
	dashHost := creds.adminHost
	if dashHost == "0.0.0.0" || dashHost == "" {
		dashHost = "127.0.0.1"
	}
	dashPort := creds.adminPort
	if dashPort == "" {
		dashPort = "9443"
	}
	fmt.Printf("  Config:    %s\n", cfgPath)
	fmt.Printf("             sudo nano %s   # edit\n", cfgPath)
	fmt.Printf("             sudo systemctl reload uwas    # apply changes (no downtime)\n")
	fmt.Println()
	fmt.Printf("  Dashboard: http://%s:%s/_uwas/dashboard/\n", dashHost, dashPort)
	if creds.apiKey != "" {
		fmt.Printf("  API Key:   %s\n", creds.apiKey)
	}
	if creds.pinCode != "" {
		fmt.Printf("  Pin Code:  %s\n", creds.pinCode)
	}
	fmt.Println()
	fmt.Println("  Service:")
	if !doStart {
		fmt.Println("    sudo systemctl start uwas      # start now (autostarts on boot)")
	}
	fmt.Println("    sudo systemctl status uwas")
	fmt.Println("    sudo journalctl -u uwas -f     # tail logs")
	fmt.Println()
	fmt.Println("  Diagnose:  uwas doctor")
	fmt.Println()

	return nil
}

// configCreds is the small slice of /etc/uwas/uwas.yaml that the installer
// surfaces back to the operator. Empty fields are treated as "not present
// in the config" — the summary suppresses the line rather than printing
// blanks.
type configCreds struct {
	apiKey    string
	pinCode   string
	adminHost string // listen host (e.g. 127.0.0.1)
	adminPort string // listen port (e.g. 9443)
}

// extractCredsFromConfig does a minimal line-scan of /etc/uwas/uwas.yaml to
// surface the dashboard URL + api_key + pin_code for the post-install
// summary. Deliberately avoids pulling in the full config package so that
// installs against a future config shape don't break — fields not found
// stay empty and the summary degrades gracefully.
func extractCredsFromConfig(path string) configCreds {
	var c configCreds
	data, err := installOsReadFile(path)
	if err != nil {
		return c
	}
	inAdmin := false
	scan := bufio.NewScanner(strings.NewReader(string(data)))
	for scan.Scan() {
		raw := scan.Text()
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		// Top-level "admin:" inside global:
		if indent == 2 && trimmed == "admin:" {
			inAdmin = true
			continue
		}
		// Leaving the admin: block (a new top-level or sibling key at indent ≤ 2).
		if inAdmin && indent <= 2 && trimmed != "" && !strings.HasPrefix(trimmed, "#") && trimmed != "admin:" {
			inAdmin = false
		}
		if !inAdmin {
			continue
		}
		// Parse key: value (strip optional surrounding quotes from value).
		colon := strings.Index(trimmed, ":")
		if colon == -1 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])
		val = strings.TrimPrefix(strings.TrimSuffix(val, `"`), `"`)
		val = strings.TrimPrefix(strings.TrimSuffix(val, `'`), `'`)
		switch key {
		case "api_key":
			c.apiKey = val
		case "pin_code":
			c.pinCode = val
		case "listen":
			// "host:port" or ":port"
			if idx := strings.LastIndex(val, ":"); idx != -1 {
				c.adminHost = val[:idx]
				c.adminPort = val[idx+1:]
			}
		}
	}
	return c
}


// UninstallCmd removes UWAS service and binary.
type UninstallCmd struct{}

func (c *UninstallCmd) Name() string { return "uninstall" }
func (c *UninstallCmd) Description() string {
	return "Remove UWAS service and binary (keeps config and data)"
}
func (c *UninstallCmd) Run(args []string) error {
	if installRuntimeGOOS != "linux" {
		return fmt.Errorf("uninstall is only supported on Linux")
	}
	if installOsGetuid() != 0 {
		return fmt.Errorf("uninstall requires root — run with sudo")
	}

	fmt.Println("UWAS Uninstaller")
	fmt.Println()
	fmt.Println("This will remove:")
	fmt.Println("  - /usr/local/bin/uwas (binary)")
	fmt.Println("  - /usr/bin/uwas (symlink)")
	fmt.Println("  - /etc/systemd/system/uwas.service")
	fmt.Println()
	fmt.Println("Config (/etc/uwas/) and data (/var/www/) will be preserved.")
	fmt.Println()
	fmt.Print("Continue? [y/N] ")

	var reply string
	fmt.Scanln(&reply)
	if reply != "y" && reply != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}

	// Stop and disable service
	installExecCommand("systemctl", "stop", "uwas").Run()
	installExecCommand("systemctl", "disable", "uwas").Run()
	fmt.Println("  - Service stopped and disabled")

	// Remove files
	installOsRemove("/etc/systemd/system/uwas.service")
	fmt.Println("  - Removed systemd service")

	installOsRemove("/usr/bin/uwas")
	fmt.Println("  - Removed /usr/bin/uwas symlink")

	self, _ := installOsExecutable()
	if self != "/usr/local/bin/uwas" {
		installOsRemove("/usr/local/bin/uwas")
		fmt.Println("  - Removed /usr/local/bin/uwas")
	} else {
		// Can't delete ourselves while running — schedule deletion
		fmt.Println("  - Binary will be removed on next reboot (currently running)")
	}

	installExecCommand("systemctl", "daemon-reload").Run()

	fmt.Println()
	fmt.Println("UWAS uninstalled. Config preserved at /etc/uwas/")
	fmt.Println("To remove everything: rm -rf /etc/uwas /var/www")
	return nil
}

// DoctorCommand runs diagnostics on the system.
func DoctorCommand(args []string) error {
	fmt.Println("UWAS Doctor — System Diagnostics")
	fmt.Println("=================================")
	fmt.Println()

	// Find config path
	configPath := ""
	for _, p := range []string{"/etc/uwas/uwas.yaml", "./uwas.yaml", os.Getenv("HOME") + "/.uwas/uwas.yaml"} {
		if _, err := os.Stat(p); err == nil {
			configPath = p
			break
		}
	}

	autoFix := false
	for _, arg := range args {
		if arg == "--fix" || arg == "-f" {
			autoFix = true
		}
	}

	// Import doctor package inline to avoid circular deps
	// We'll call it via the admin package or directly
	fmt.Printf("Config: %s\n", configPath)
	if autoFix {
		fmt.Println("Mode: AUTO-FIX enabled")
	} else {
		fmt.Println("Mode: Diagnose only (use --fix to auto-repair)")
	}
	fmt.Println()

	// Run checks directly using exec to call our own binary's API
	// Or implement checks here
	checks := runDoctorChecks(configPath, autoFix)

	ok, warn, fail, fixed := 0, 0, 0, 0
	for _, c := range checks {
		icon := "✓"
		switch c.status {
		case "ok":
			icon = "✓"
			ok++
		case "warn":
			icon = "⚠"
			warn++
		case "fail":
			icon = "✗"
			fail++
		case "fixed":
			icon = "★"
			fixed++
		}
		fmt.Printf("  %s %-20s %s\n", icon, c.name, c.message)
		if c.howTo != "" {
			fmt.Printf("    → %s\n", c.howTo)
		}
		if c.fix != "" {
			fmt.Printf("    ★ Auto-fixed: %s\n", c.fix)
		}
	}

	fmt.Println()
	fmt.Printf("Summary: %d ok, %d warnings, %d failures, %d fixed\n", ok, warn, fail, fixed)
	return nil
}

type cliCheck struct {
	name, status, message, howTo, fix string
}

func runDoctorChecks(configPath string, autoFix bool) []cliCheck {
	var checks []cliCheck

	// OS
	checks = append(checks, cliCheck{name: "OS", status: "ok", message: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)})

	// PHP-FPM
	checks = append(checks, checkCLI_PHPFPM(autoFix))
	checks = append(checks, checkCLI_PHPModules())
	checks = append(checks, checkCLI_MySQL(autoFix))
	checks = append(checks, checkCLI_WebRoot(autoFix))
	checks = append(checks, checkCLI_Config(configPath))
	checks = append(checks, checkCLI_Disk())
	checks = append(checks, checkCLI_DNS())

	return checks
}

// Hooks for doctor check testing.
var (
	doctorExecCommand  = exec.Command
	doctorExecLookPath = exec.LookPath
	doctorOsStat       = os.Stat
)

func checkCLI_PHPFPM(autoFix bool) cliCheck {
	sockets := []string{"/run/php/php8.4-fpm.sock", "/run/php/php8.3-fpm.sock", "/run/php/php8.2-fpm.sock"}
	for _, sock := range sockets {
		if _, err := doctorOsStat(sock); err == nil {
			return cliCheck{name: "PHP-FPM", status: "ok", message: "Running at " + sock}
		}
	}
	if _, err := doctorExecLookPath("php-fpm8.3"); err == nil {
		if autoFix {
			os.MkdirAll("/run/php", 0755)
			doctorExecCommand("systemctl", "start", "php8.3-fpm").Run()
			return cliCheck{name: "PHP-FPM", status: "fixed", message: "Started php8.3-fpm", fix: "systemctl start php8.3-fpm"}
		}
		return cliCheck{name: "PHP-FPM", status: "fail", message: "Installed but not running", howTo: "sudo systemctl start php8.3-fpm"}
	}
	return cliCheck{name: "PHP-FPM", status: "fail", message: "Not installed", howTo: "sudo apt install php8.3-fpm php8.3-mysql php8.3-curl php8.3-gd php8.3-mbstring php8.3-xml"}
}

func checkCLI_PHPModules() cliCheck {
	out, err := doctorExecCommand("php", "-m").Output()
	if err != nil {
		return cliCheck{name: "PHP Modules", status: "warn", message: "Cannot check"}
	}
	mods := string(out)
	missing := []string{}
	for _, m := range []string{"mysqli", "curl", "gd", "mbstring", "xml"} {
		if !containsCI(mods, m) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		return cliCheck{name: "PHP Modules", status: "warn", message: "Missing: " + joinStr(missing), howTo: "sudo apt install " + phpPkgs(missing)}
	}
	return cliCheck{name: "PHP Modules", status: "ok", message: "All required modules present"}
}

func checkCLI_MySQL(autoFix bool) cliCheck {
	out, _ := doctorExecCommand("systemctl", "is-active", "mariadb").Output()
	if containsCI(string(out), "active") {
		return cliCheck{name: "MariaDB", status: "ok", message: "Running"}
	}
	if _, err := doctorExecLookPath("mariadb"); err == nil {
		if autoFix {
			os.MkdirAll("/run/mysqld", 0755)
			doctorExecCommand("chown", "mysql:mysql", "/run/mysqld").Run()
			doctorExecCommand("systemctl", "start", "mariadb").Run()
			return cliCheck{name: "MariaDB", status: "fixed", message: "Started", fix: "systemctl start mariadb"}
		}
		return cliCheck{name: "MariaDB", status: "fail", message: "Not running", howTo: "sudo systemctl start mariadb"}
	}
	return cliCheck{name: "MariaDB", status: "warn", message: "Not installed", howTo: "sudo apt install mariadb-server"}
}

func checkCLI_WebRoot(autoFix bool) cliCheck {
	if _, err := doctorOsStat("/var/www"); err == nil {
		return cliCheck{name: "Web Root", status: "ok", message: "/var/www"}
	}
	if autoFix {
		os.MkdirAll("/var/www", 0755)
		doctorExecCommand("chown", "www-data:www-data", "/var/www").Run()
		return cliCheck{name: "Web Root", status: "fixed", message: "Created /var/www", fix: "mkdir /var/www"}
	}
	return cliCheck{name: "Web Root", status: "fail", message: "/var/www missing", howTo: "sudo mkdir -p /var/www && sudo chown www-data:www-data /var/www"}
}

func checkCLI_Config(path string) cliCheck {
	if path != "" {
		return cliCheck{name: "Config", status: "ok", message: path}
	}
	return cliCheck{name: "Config", status: "warn", message: "No config found", howTo: "Run 'uwas serve' to create initial config"}
}

func checkCLI_Disk() cliCheck {
	out, err := doctorExecCommand("df", "-h", "/").Output()
	if err != nil {
		return cliCheck{name: "Disk", status: "ok", message: "OK"}
	}
	lines := string(out)
	fields := splitFields(lines)
	if len(fields) > 4 {
		return cliCheck{name: "Disk", status: "ok", message: fields[4] + " used"}
	}
	return cliCheck{name: "Disk", status: "ok", message: "OK"}
}

func checkCLI_DNS() cliCheck {
	_, err := doctorExecCommand("dig", "+short", "acme-v02.api.letsencrypt.org").Output()
	if err != nil {
		return cliCheck{name: "DNS", status: "warn", message: "dig not available"}
	}
	return cliCheck{name: "DNS", status: "ok", message: "Working"}
}

func containsCI(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || func() bool {
		sl := toLower(s)
		return indexStr(sl, toLower(sub)) >= 0
	}())
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b[i] = s[i] + 32
		} else {
			b[i] = s[i]
		}
	}
	return string(b)
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func joinStr(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}

func phpPkgs(mods []string) string {
	result := ""
	for i, m := range mods {
		if i > 0 {
			result += " "
		}
		result += "php-" + toLower(m)
	}
	return result
}

func splitFields(s string) []string {
	lines := make([]string, 0)
	for _, line := range splitLines(s) {
		lines = append(lines, splitWhitespace(line)...)
	}
	return lines
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitWhitespace(s string) []string {
	var fields []string
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
		} else {
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}
