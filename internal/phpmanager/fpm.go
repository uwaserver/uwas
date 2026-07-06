// PHP-FPM process lifecycle. Split out of manager.go per refactor.md
// A14. Owns starting a php-cgi daemon for a given version
// (StartFPM, startFPMDaemon), stopping / restarting it, listing live
// processes via Status, and the per-process probes (isProcessRunning,
// fileExists) the rest of the package leans on.
package phpmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// StartFPM starts a php-cgi process listening on the given address (e.g. "127.0.0.1:9000").
// It uses `php-cgi -b <addr>` as a lightweight FastCGI server.
func (m *Manager) StartFPM(version, listenAddr string) error {
	// Atomically reserve the version slot up-front. Load-then-Store had a TOCTOU:
	// two concurrent StartFPM calls for the same version both passed the Load
	// check and both spawned, leaking one untracked process. The placeholder is
	// replaced by the real processInfo on success and released on any failure.
	if _, loaded := m.processes.LoadOrStore(version, &processInfo{listenAddr: listenAddr}); loaded {
		return fmt.Errorf("PHP %s is already running", version)
	}
	release := func() { m.processes.Delete(version) }

	inst, ok := m.findInstall(version)
	if !ok {
		release()
		return fmt.Errorf("PHP %s not found", version)
	}
	if inst.SAPI != "cgi-fcgi" && inst.SAPI != "fpm-fcgi" {
		release()
		return fmt.Errorf("PHP %s binary %s is %s, not cgi-fcgi — install php-cgi or php-fpm", version, inst.Binary, inst.SAPI)
	}

	// If php-fpm binary exists, prefer it (proper process manager). On success
	// startFPMDaemon replaces the reservation with the real process entry.
	fpmBinary := strings.Replace(inst.Binary, "php-cgi", "php-fpm", 1)
	if inst.SAPI == "fpm-fcgi" || fileExists(fpmBinary) {
		if err := m.startFPMDaemon(version, fpmBinary, listenAddr); err != nil {
			release()
			return err
		}
		return nil
	}

	cmd := m.execCommand(inst.Binary, "-b", listenAddr)
	// PHP_FCGI_CHILDREN: spawn N worker children for parallel requests.
	// Without this, php-cgi handles only 1 request at a time!
	cmd.Env = append(os.Environ(),
		"PHP_FCGI_CHILDREN=8",
		"PHP_FCGI_MAX_REQUESTS=500",
	)
	cmd.Stdout = m.logger.Writer(4) // slog.LevelInfo
	cmd.Stderr = m.logger.Writer(8) // slog.LevelError

	if err := cmd.Start(); err != nil {
		release()
		return fmt.Errorf("start php-cgi: %w", err)
	}

	m.processes.Store(version, &processInfo{
		cmd:        cmd,
		listenAddr: listenAddr,
	})

	m.logger.Info("started PHP-CGI", "version", version, "listen", listenAddr, "pid", cmd.Process.Pid)

	// Reap the process in the background.
	go func() {
		err := cmd.Wait()
		m.processes.Delete(version)
		if err != nil {
			m.logger.Warn("PHP-CGI exited", "version", version, "error", err)
		} else {
			m.logger.Info("PHP-CGI exited", "version", version)
		}
	}()

	return nil
}

// startFPMDaemon starts php-fpm as a proper daemon with worker pool.
func (m *Manager) startFPMDaemon(version, binary, listenAddr string) error {
	// Generate a minimal php-fpm config for this listen address
	confDir := filepath.Join(os.TempDir(), "uwas-fpm")
	osMkdirAllHook(confDir, 0755)
	confPath := filepath.Join(confDir, fmt.Sprintf("php%s-fpm.conf", strings.ReplaceAll(version, ".", "")))

	conf := fmt.Sprintf(`[global]
pid = %s/php%s-fpm.pid
error_log = /dev/stderr
daemonize = no

[www]
listen = %s
pm = dynamic
pm.max_children = 10
pm.start_servers = 4
pm.min_spare_servers = 2
pm.max_spare_servers = 6
pm.max_requests = 500
`, confDir, strings.ReplaceAll(version, ".", ""), listenAddr)

	if err := osWriteFileHook(confPath, []byte(conf), 0644); err != nil {
		return fmt.Errorf("write fpm config: %w", err)
	}

	cmd := m.execCommand(binary, "--fpm-config", confPath, "--nodaemonize")
	cmd.Stdout = m.logger.Writer(4)
	cmd.Stderr = m.logger.Writer(8)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start php-fpm: %w", err)
	}

	m.processes.Store(version, &processInfo{
		cmd:        cmd,
		listenAddr: listenAddr,
	})

	m.logger.Info("started PHP-FPM", "version", version, "listen", listenAddr, "pid", cmd.Process.Pid, "workers", 10)

	go func() {
		err := cmd.Wait()
		m.processes.Delete(version)
		if err != nil {
			m.logger.Warn("PHP-FPM exited", "version", version, "error", err)
		}
	}()

	return nil
}

func fileExists(path string) bool {
	_, err := osStat(path)
	return err == nil
}

// StopFPM stops the PHP-CGI process for the given version.
func (m *Manager) StopFPM(version string) error {
	val, ok := m.processes.Load(version)
	if !ok {
		return fmt.Errorf("PHP %s is not running", version)
	}

	info, ok := val.(*processInfo)
	if !ok || info == nil || info.cmd == nil || info.cmd.Process == nil {
		m.processes.Delete(version)
		m.logger.Warn("stale PHP-CGI process entry removed", "version", version)
		return nil
	}
	if err := info.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill php-cgi: %w", err)
	}

	m.processes.Delete(version)
	m.logger.Info("stopped PHP-CGI", "version", version)
	return nil
}

// RestartFPM gracefully restarts the PHP process for the given version.
// It stops the running process and starts a new one with the same listen address,
// ensuring that updated php.ini settings take effect.
func (m *Manager) RestartFPM(version string) error {
	val, ok := m.processes.Load(version)
	if !ok {
		return fmt.Errorf("PHP %s is not running", version)
	}
	info, ok := val.(*processInfo)
	if !ok || info == nil {
		m.processes.Delete(version)
		return fmt.Errorf("PHP %s has stale process entry", version)
	}
	listenAddr := info.listenAddr

	if err := m.StopFPM(version); err != nil {
		return fmt.Errorf("restart: stop failed: %w", err)
	}
	if err := m.StartFPM(version, listenAddr); err != nil {
		return fmt.Errorf("restart: start failed: %w", err)
	}
	m.logger.Info("restarted PHP", "version", version, "listen", listenAddr)
	return nil
}

// PHPStatus represents the status of a single PHP installation.
type PHPStatus struct {
	PHPInstall
	Running       bool     `json:"running"`
	ListenAddr    string   `json:"listen_addr,omitempty"`
	SocketPath    string   `json:"socket_path,omitempty"`
	PID           int      `json:"pid,omitempty"`
	SystemManaged bool     `json:"system_managed,omitempty"`
	DomainCount   int      `json:"domain_count"`      // number of domains using this version
	Domains       []string `json:"domains,omitempty"` // domain names using this version
}

// Status returns the status of all detected PHP installations, including
// whether each one has a running subprocess or system php-fpm socket.
func (m *Manager) Status() []PHPStatus {
	m.mu.RLock()
	installs := make([]PHPInstall, len(m.installations))
	copy(installs, m.installations)
	m.mu.RUnlock()

	var statuses []PHPStatus
	for _, inst := range installs {
		// Skip CLI binaries — they can't serve FastCGI.
		if inst.SAPI == "cli" {
			continue
		}
		st := PHPStatus{PHPInstall: inst}

		// Check for UWAS-managed process
		if val, ok := m.processes.Load(inst.Version); ok {
			info := val.(*processInfo)
			// Verify the process is actually running
			if info.cmd != nil && info.cmd.Process != nil && m.isProcessRunning(info.cmd.Process.Pid) {
				st.Running = true
				st.ListenAddr = info.listenAddr
				st.PID = info.cmd.Process.Pid
			} else {
				// Process is dead, clean up
				m.processes.Delete(inst.Version)
			}
		}

		// Check for system php-fpm socket (only for FPM SAPI)
		if strings.Contains(inst.SAPI, "fpm") {
			sock := m.detectSystemFPMSocket(inst.Version)
			if sock != "" {
				st.SocketPath = sock
				st.Running = true
				st.SystemManaged = true
				if st.ListenAddr == "" {
					st.ListenAddr = sock
				}
			}
		}

		// Count domains using this PHP version (match by short version e.g. "8.3")
		short := inst.Version
		if parts := strings.SplitN(inst.Version, ".", 3); len(parts) >= 2 {
			short = parts[0] + "." + parts[1]
		}
		m.domainMu.RLock()
		for domain, di := range m.domainMap {
			diShort := di.version
			if parts := strings.SplitN(di.version, ".", 3); len(parts) >= 2 {
				diShort = parts[0] + "." + parts[1]
			}
			if diShort == short {
				st.DomainCount++
				st.Domains = append(st.Domains, domain)
			}
		}
		m.domainMu.RUnlock()

		statuses = append(statuses, st)
	}
	return statuses
}

// isProcessRunning checks if a process with the given PID is actually running.
func (m *Manager) isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Try to find the process - this works on both Unix and Windows
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 checks if process exists without sending a real signal
	// On Windows, this may not work, so we just rely on FindProcess success
	if runtime.GOOS != "windows" {
		return p.Signal(syscall.Signal(0)) == nil
	}
	// On Windows, FindProcess succeeding is enough indication the process exists
	return true
}
