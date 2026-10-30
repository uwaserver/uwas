// Package services manages system services (start/stop/restart/status).
package services

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Testable hooks
var (
	execCommandFn = exec.Command
	runtimeGOOS   = runtime.GOOS

	// statusTimeout bounds each systemctl status query. The listing is polled by
	// the dashboard, and a wedged systemd would otherwise pin a request, a
	// goroutine and a systemctl process per poll (F5901).
	statusTimeout = 5 * time.Second
)

var errStatusTimeout = errors.New("systemctl did not answer in time")

// actionTimeout bounds start/stop/restart/enable. systemd's own default unit
// start/stop timeout is 90 s, so this only fires on a wedged systemd (F57001).
var actionTimeout = 2 * time.Minute

var errActionTimeout = errors.New("systemctl did not finish in time")

// ErrUnknownService is returned for a name outside the KnownServices allowlist.
var ErrUnknownService = errors.New("unknown service")

// outputWithin is cmd.Output with a deadline: a command still running after
// statusTimeout is killed and errStatusTimeout returned. Exit errors keep their
// *exec.ExitError type.
func outputWithin(cmd *exec.Cmd) ([]byte, error) {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.WaitDelay = time.Second // do not wait on pipes a leftover child holds open
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(statusTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return buf.Bytes(), err
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return nil, errStatusTimeout
	}
}

// runWithin is cmd.Run with the actionTimeout deadline; a command still
// running after it is killed and errActionTimeout returned.
func runWithin(cmd *exec.Cmd) error {
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(actionTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return errActionTimeout
	}
}

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
	{"redis-server", "Redis", []string{"redis"}},
	{"memcached", "Memcached", nil},
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
		svc, timedOut := checkServiceTimed(ks.Name, ks.Display)
		if timedOut {
			break // systemd is not answering; the rest would hang the same way
		}
		if svc == nil {
			// Try aliases
			for _, alias := range ks.Aliases {
				svc, timedOut = checkServiceTimed(alias, ks.Display)
				if timedOut || svc != nil {
					break
				}
			}
			if timedOut {
				break
			}
		}
		if svc != nil {
			result = append(result, *svc)
		}
	}
	return result
}

func checkService(name, display string) *Service {
	svc, _ := checkServiceTimed(name, display)
	return svc
}

// checkServiceTimed is checkService that also reports a systemctl that did not
// answer in time.
func checkServiceTimed(name, display string) (*Service, bool) {
	// Check if service unit exists
	out, err := outputWithin(execCommandFn("systemctl", "is-active", name))
	if errors.Is(err, errStatusTimeout) {
		return nil, true
	}
	active := strings.TrimSpace(string(out))
	if err != nil {
		// `systemctl is-active` exits 0 when the unit is active, 3 when the
		// unit is loaded but not running (inactive, failed, activating) and 4
		// when the unit is not known at all. Only the last one means "not
		// installed", so bailing out on every non-zero exit silently dropped
		// stopped and crashed services from ListServices — the dashboard
		// showed them as absent rather than down, even though Service.Active
		// is documented to carry exactly those values.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
			return nil, false
		}
	}

	enabled := false
	enabledOut, err := outputWithin(execCommandFn("systemctl", "is-enabled", name))
	if errors.Is(err, errStatusTimeout) {
		return nil, true
	}
	if strings.TrimSpace(string(enabledOut)) == "enabled" {
		enabled = true
	}

	return &Service{
		Name:    name,
		Display: display,
		Running: active == "active",
		Enabled: enabled,
		Active:  active,
	}, false
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
		return fmt.Errorf("%w: %s", ErrUnknownService, name)
	}
	if err := runWithin(execCommandFn("systemctl", "start", name)); err != nil {
		return err
	}
	return runWithin(execCommandFn("systemctl", "enable", name))
}

// StopService stops a systemd service (must be in KnownServices allowlist).
func StopService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("%w: %s", ErrUnknownService, name)
	}
	return runWithin(execCommandFn("systemctl", "stop", name))
}

// RestartService restarts a systemd service (must be in KnownServices allowlist).
func RestartService(name string) error {
	if !IsKnownService(name) {
		return fmt.Errorf("%w: %s", ErrUnknownService, name)
	}
	return runWithin(execCommandFn("systemctl", "restart", name))
}
