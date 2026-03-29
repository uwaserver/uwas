package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/uwaserver/uwas/internal/phpmanager"
)

// Hooks for testing.
var (
	conflictsRuntimeGOOS  = runtime.GOOS
	conflictsExecLookPath = exec.LookPath
	conflictsExecCommand  = exec.Command
)

// ConflictingServer describes a detected web server that may conflict with UWAS.
type ConflictingServer struct {
	Name    string // "apache2", "nginx", "caddy", "lighttpd"
	Running bool
	PID     string
	Service string // systemd service name
}

// DetectConflicts checks for other web servers installed or running on the system.
func DetectConflicts() []ConflictingServer {
	if conflictsRuntimeGOOS == "windows" {
		return nil
	}

	type candidate struct {
		name     string
		bins     []string
		services []string
	}

	candidates := []candidate{
		{"Apache", []string{"apache2", "httpd"}, []string{"apache2", "httpd"}},
		{"Nginx", []string{"nginx"}, []string{"nginx"}},
		{"Caddy", []string{"caddy"}, []string{"caddy"}},
		{"Lighttpd", []string{"lighttpd"}, []string{"lighttpd"}},
	}

	var found []ConflictingServer

	for _, c := range candidates {
		installed := false
		for _, bin := range c.bins {
			if _, err := conflictsExecLookPath(bin); err == nil {
				installed = true
				break
			}
		}
		if !installed {
			continue
		}

		cs := ConflictingServer{
			Name:    c.name,
			Service: c.services[0],
		}

		// Check if running via pidof.
		for _, bin := range c.bins {
			out, err := conflictsExecCommand("pidof", bin).Output()
			if err == nil && len(strings.TrimSpace(string(out))) > 0 {
				cs.Running = true
				cs.PID = strings.Fields(strings.TrimSpace(string(out)))[0]
				break
			}
		}

		// Fallback: check service activity (handles hosts where pidof misses it).
		if !cs.Running {
			for _, svc := range c.services {
				out, err := conflictsExecCommand("systemctl", "is-active", svc).Output()
				if err == nil && strings.TrimSpace(string(out)) == "active" {
					cs.Running = true
					cs.Service = svc
					if out, err := conflictsExecCommand("systemctl", "show", "--property", "MainPID", "--value", svc).Output(); err == nil {
						pid := strings.TrimSpace(string(out))
						if pid != "" && pid != "0" {
							cs.PID = pid
						}
					}
					break
				}
			}
		}

		found = append(found, cs)
	}

	return found
}

// PrintConflicts displays detected conflicts and returns true if any are running.
func PrintConflicts(conflicts []ConflictingServer) bool {
	if len(conflicts) == 0 {
		return false
	}

	hasRunning := false
	for _, c := range conflicts {
		if c.Running {
			hasRunning = true
			break
		}
	}

	if hasRunning {
		fmt.Println("  \033[33m!\033[0m Other web servers detected:")
		fmt.Println()
	} else {
		fmt.Println("  \033[36mi\033[0m Other web servers installed (not running):")
		fmt.Println()
	}

	for _, c := range conflicts {
		status := "\033[32mstopped\033[0m"
		if c.Running {
			status = fmt.Sprintf("\033[31mrunning (PID %s)\033[0m", c.PID)
		}
		fmt.Printf("    %-12s %s\n", c.Name, status)
	}
	fmt.Println()

	return hasRunning
}

// OfferStopConflicts asks the user whether to stop running conflicts.
func OfferStopConflicts(conflicts []ConflictingServer) {
	var running []ConflictingServer
	for _, c := range conflicts {
		if c.Running {
			running = append(running, c)
		}
	}
	if len(running) == 0 {
		return
	}

	fmt.Println("  UWAS needs ports 80/443. These servers may conflict.")
	choice := promptWithDefault("  Stop and disable them? (y/n)", "y")
	if !strings.EqualFold(choice, "y") && !strings.EqualFold(choice, "yes") {
		fmt.Println("  Skipped. You may need to stop them manually if ports conflict.")
		return
	}

	for _, c := range running {
		fmt.Printf("  Stopping %s...\n", c.Name)
		// Try systemctl first
		if err := conflictsExecCommand("systemctl", "stop", c.Service).Run(); err == nil {
			conflictsExecCommand("systemctl", "disable", c.Service).Run()
			fmt.Printf("    \033[32m✓\033[0m %s stopped and disabled\n", c.Name)
		} else {
			// Fallback: service command
			if err := conflictsExecCommand("service", c.Service, "stop").Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s stopped\n", c.Name)
			} else {
				fmt.Printf("    \033[31m✗\033[0m Could not stop %s: %v\n", c.Name, err)
			}
		}
	}

	// Offer to uninstall
	fmt.Println()
	uninstall := promptWithDefault("  Uninstall them completely? (y/n)", "n")
	if strings.EqualFold(uninstall, "y") || strings.EqualFold(uninstall, "yes") {
		for _, c := range running {
			fmt.Printf("  Removing %s...\n", c.Name)
			if err := conflictsExecCommand("apt", "remove", "-y", c.Service).Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s removed\n", c.Name)
			} else if err := conflictsExecCommand("dnf", "remove", "-y", c.Service).Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s removed\n", c.Name)
			} else if err := conflictsExecCommand("apk", "del", c.Service).Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s removed\n", c.Name)
			} else {
				fmt.Printf("    \033[31m✗\033[0m Could not remove %s — try: sudo apt remove %s\n", c.Name, c.Service)
			}
		}
	}
	fmt.Println()
}

// OfferPHPInstall checks if PHP-CGI/FPM is available and offers to install it.
func OfferPHPInstall() {
	if conflictsRuntimeGOOS == "windows" {
		return
	}

	// Quick check: is any php-cgi or php-fpm binary on PATH?
	hasCGI := false
	for _, bin := range []string{"php-cgi", "php-cgi8.4", "php-cgi8.3", "php-cgi8.2", "php-cgi8.1"} {
		if _, err := conflictsExecLookPath(bin); err == nil {
			hasCGI = true
			break
		}
	}
	for _, bin := range []string{"php-fpm", "php-fpm8.4", "php-fpm8.3", "php-fpm8.2", "php-fpm8.1"} {
		if _, err := conflictsExecLookPath(bin); err == nil {
			hasCGI = true
			break
		}
	}

	if hasCGI {
		return // PHP already available
	}

	fmt.Println("  \033[33m!\033[0m No PHP (FastCGI/FPM) detected on this system.")
	fmt.Println("    PHP is needed to serve WordPress, Laravel, and other PHP sites.")
	fmt.Println()

	versions := []string{"8.5", "8.4", "8.3", "8.2"}
	fmt.Println("    Available versions to install:")
	for i, v := range versions {
		tag := ""
		if v == "8.5" {
			tag = " \033[32m(latest)\033[0m"
		} else if v == "8.4" {
			tag = " \033[36m(stable)\033[0m"
		} else if v == "8.3" {
			tag = " \033[36m(LTS)\033[0m"
		}
		fmt.Printf("      %d) PHP %s%s\n", i+1, v, tag)
	}
	fmt.Println("      s) Skip — I'll install PHP later")
	fmt.Println()

	choice := promptWithDefault("  Install PHP version", "s")

	var version string
	switch strings.TrimSpace(choice) {
	case "1", "8.5":
		version = "8.5"
	case "2", "8.4":
		version = "8.4"
	case "3", "8.3":
		version = "8.3"
	case "4", "8.2":
		version = "8.2"
	case "s", "S", "":
		fmt.Println("  Skipped. You can install later with: uwas php install 8.3")
		fmt.Println()
		return
	default:
		// Treat as version string if it looks like one
		if strings.Contains(choice, ".") {
			version = choice
		} else {
			fmt.Println("  Skipped.")
			fmt.Println()
			return
		}
	}

	info := phpmanager.GetInstallInfo(version)
	fmt.Printf("\n  Installing PHP %s on %s...\n\n", version, info.Distro)
	for _, cmd := range info.Commands {
		fmt.Printf("    > %s\n", cmd)
	}
	fmt.Println()

	confirm := promptWithDefault("  Run these commands now? (y/n)", "y")
	if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
		fmt.Println("  Skipped. Run manually or use: uwas php install " + version)
		fmt.Println()
		return
	}

	fmt.Println("  Running install (this may take a minute)...")
	output, err := phpmanager.RunInstall(version)
	if output != "" {
		// Show last few lines only
		lines := strings.Split(strings.TrimSpace(output), "\n")
		start := 0
		if len(lines) > 10 {
			start = len(lines) - 10
		}
		for _, l := range lines[start:] {
			fmt.Printf("    %s\n", l)
		}
	}
	if err != nil {
		fmt.Printf("  \033[31m✗\033[0m Install failed: %v\n", err)
		fmt.Println("    Try manually: uwas php install-info " + version)
	} else {
		fmt.Printf("  \033[32m✓\033[0m PHP %s installed successfully!\n", version)
	}
	fmt.Println()
}
