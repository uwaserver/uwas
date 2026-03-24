// Package services manages system services (start/stop/restart/status).
package services

import (
	"os/exec"
	"runtime"
	"strings"
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
	{"php8.3-fpm", "PHP 8.3 FPM", []string{"php-fpm", "php8.4-fpm", "php8.2-fpm"}},
	{"cron", "Cron", []string{"crond"}},
	{"ufw", "Firewall (UFW)", nil},
	{"fail2ban", "Fail2ban", nil},
	{"postfix", "Postfix (Mail)", nil},
	{"dovecot", "Dovecot (IMAP)", nil},
	{"redis-server", "Redis", []string{"redis"}},
	{"memcached", "Memcached", nil},
}

// ListServices returns the status of all known system services.
func ListServices() []Service {
	if runtime.GOOS == "windows" {
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
	out, err := exec.Command("systemctl", "is-active", name).Output()
	if err != nil {
		return nil
	}
	active := strings.TrimSpace(string(out))

	enabled := false
	enabledOut, _ := exec.Command("systemctl", "is-enabled", name).Output()
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

// StartService starts a systemd service.
func StartService(name string) error {
	if err := exec.Command("systemctl", "start", name).Run(); err != nil {
		return err
	}
	return exec.Command("systemctl", "enable", name).Run()
}

// StopService stops a systemd service.
func StopService(name string) error {
	return exec.Command("systemctl", "stop", name).Run()
}

// RestartService restarts a systemd service.
func RestartService(name string) error {
	return exec.Command("systemctl", "restart", name).Run()
}

// EnableService enables a service to start on boot.
func EnableService(name string) error {
	return exec.Command("systemctl", "enable", name).Run()
}

// DisableService disables a service from starting on boot.
func DisableService(name string) error {
	return exec.Command("systemctl", "disable", name).Run()
}
