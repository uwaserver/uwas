package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
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
	if runtime.GOOS == "windows" {
		return nil
	}

	type candidate struct {
		name    string
		bins    []string
		service string
	}

	candidates := []candidate{
		{"Apache", []string{"apache2", "httpd"}, "apache2"},
		{"Nginx", []string{"nginx"}, "nginx"},
		{"Caddy", []string{"caddy"}, "caddy"},
		{"Lighttpd", []string{"lighttpd"}, "lighttpd"},
	}

	var found []ConflictingServer

	for _, c := range candidates {
		installed := false
		for _, bin := range c.bins {
			if _, err := exec.LookPath(bin); err == nil {
				installed = true
				break
			}
		}
		if !installed {
			continue
		}

		cs := ConflictingServer{
			Name:    c.name,
			Service: c.service,
		}

		// Check if running via pidof or systemctl
		for _, bin := range c.bins {
			out, err := exec.Command("pidof", bin).Output()
			if err == nil && len(strings.TrimSpace(string(out))) > 0 {
				cs.Running = true
				cs.PID = strings.Fields(strings.TrimSpace(string(out)))[0]
				break
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
		if err := exec.Command("systemctl", "stop", c.Service).Run(); err == nil {
			exec.Command("systemctl", "disable", c.Service).Run()
			fmt.Printf("    \033[32m✓\033[0m %s stopped and disabled\n", c.Name)
		} else {
			// Fallback: service command
			if err := exec.Command("service", c.Service, "stop").Run(); err == nil {
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
			if err := exec.Command("apt", "remove", "-y", c.Service).Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s removed\n", c.Name)
			} else if err := exec.Command("dnf", "remove", "-y", c.Service).Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s removed\n", c.Name)
			} else if err := exec.Command("apk", "del", c.Service).Run(); err == nil {
				fmt.Printf("    \033[32m✓\033[0m %s removed\n", c.Name)
			} else {
				fmt.Printf("    \033[31m✗\033[0m Could not remove %s — try: sudo apt remove %s\n", c.Name, c.Service)
			}
		}
	}
	fmt.Println()
}
