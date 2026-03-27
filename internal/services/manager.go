// Package services manages system services (start/stop/restart/status).
package services

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Testable hooks
var (
	execCommandFn = exec.Command
	runtimeGOOS   = runtime.GOOS
)

// Service represents a system service.
type Service struct {
	Name    string `json:"name"`
	Display string `json:"display"`
	Running bool   `json:"running"`
	Enabled bool   `json:"enabled"`
	Active  string `json:"active"` // "active", "inactive", "failed"
}

// KnownServices lists services UWAS cares about.
var KnownServices = []struct {
	Name    string
	Display string
	Aliases []string // alternative service names
}{
	{"mariadb", "MariaDB", []string{"mysql", "mysqld"}},
	{"ssh", "SSH Server", []string{"sshd"}},
	{"php8.5-fpm", "PHP 8.5 FPM", nil},
	{"php8.4-fpm", "PHP 8.4 FPM", nil},
	{"php8.3-fpm", "PHP 8.3 FPM", nil},
	{"php8.2-fpm", "PHP 8.2 FPM", nil},
	{"php8.1-fpm", "PHP 8.1 FPM", nil},
	{"docker", "Docker", []string{"containerd"}},
	{"cron", "Cron", []string{"crond"}},
	{"ufw", "Firewall (UFW)", nil},
	{"fail2ban", "Fail2ban", nil},
}

// ListServices returns the status of all known system services.
func ListServices() []Service {
	if runtimeGOOS == "windows" {
		return nil
	}

	var result []Service
	for _, ks := range KnownServices {
		svc := checkService(ks.Name, ks.Display)
		if svc == nil {
			// Try aliases
			for _, alias := range ks.Aliases {
				svc = checkService(alias, ks.Display)
				if svc != nil {
					break
				}
			}
		}
		if svc != nil {
			result = append(result, *svc)
		}
	}
	return result
}

func checkService(name, display string) *Service {
	// Check if service unit exists
	out, err := execCommandFn("systemctl", "is-active", name).Output()
	if err != nil {
		return nil
	}
	active := strings.TrimSpace(string(out))

	enabled := false
	enabledOut, _ := execCommandFn("systemctl", "is-enabled", name).Output()
	if strings.TrimSpace(string(enabledOut)) == "enabled" {
		enabled = true
	}

	return &Service{
		Name:    name,
		Display: display,
		Running: active == "active",
		Enabled: enabled,
		Active:  active,
	}
}

// IsKnownService checks if a service name is in the allowlist.
func IsKnownService(name string) bool {
	for _, ks := range KnownServices {
		if ks.Name == name {
			return true
		}
		for _, alias := range ks.Aliases {
			if alias == name {
				return true
			}
		}
	}
	return false
}

// StartService starts a systemd service (must be in KnownServices allowlist).
func StartService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("unknown service: %s", name)
	}
	if err := execCommandFn("systemctl", "start", name).Run(); err != nil {
		return err
	}
	return execCommandFn("systemctl", "enable", name).Run()
}

// StopService stops a systemd service (must be in KnownServices allowlist).
func StopService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("unknown service: %s", name)
	}
	return execCommandFn("systemctl", "stop", name).Run()
}

// RestartService restarts a systemd service (must be in KnownServices allowlist).
func RestartService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("unknown service: %s", name)
	}
	return execCommandFn("systemctl", "restart", name).Run()
}

// EnableService enables a service to start on boot.
func EnableService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("unknown service: %s", name)
	}
	return execCommandFn("systemctl", "enable", name).Run()
}

// DisableService disables a service from starting on boot.
func DisableService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("unknown service: %s", name)
	}
	return execCommandFn("systemctl", "disable", name).Run()
}
