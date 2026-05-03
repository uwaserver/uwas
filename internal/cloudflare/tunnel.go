package cloudflare

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Runner manages the lifecycle of `cloudflared tunnel run --token <T>` processes.
// One Runner is shared by the admin server.
type Runner struct {
	mu       sync.Mutex
	procs    map[string]*runningProc // tunnelID → process
	logger   *logger.Logger
	binary   string // "" → resolved at run time via PATH
}

type runningProc struct {
	tunnelID  string
	cmd       *exec.Cmd
	startedAt time.Time
	stopCh    chan struct{}
	logTail   *ringBuffer
}

// NewRunner returns a Runner that uses cloudflared from PATH.
func NewRunner(log *logger.Logger) *Runner {
	return &Runner{
		procs:  make(map[string]*runningProc),
		logger: log,
	}
}

// IsRunning reports whether the tunnel currently has a live process.
func (r *Runner) IsRunning(tunnelID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.procs[tunnelID]
	return ok && p.cmd != nil && p.cmd.Process != nil
}

// Status describes one running tunnel for the dashboard.
type Status struct {
	TunnelID  string    `json:"tunnel_id"`
	Running   bool      `json:"running"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Uptime    string    `json:"uptime,omitempty"`
}

// StatusOf returns whether the tunnel is up and basic process info.
func (r *Runner) StatusOf(tunnelID string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.procs[tunnelID]
	if !ok || p.cmd == nil || p.cmd.Process == nil {
		return Status{TunnelID: tunnelID, Running: false}
	}
	return Status{
		TunnelID:  tunnelID,
		Running:   true,
		PID:       p.cmd.Process.Pid,
		StartedAt: p.startedAt,
		Uptime:    formatUptime(time.Since(p.startedAt)),
	}
}

// Tail returns the last N lines from the tunnel's combined stdout/stderr buffer.
func (r *Runner) Tail(tunnelID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.procs[tunnelID]
	if !ok || p.logTail == nil {
		return ""
	}
	return p.logTail.String()
}

// Start spawns `cloudflared tunnel run --token <token>` and registers a monitor
// that auto-restarts on crash (up to ~ once every 2s).
func (r *Runner) Start(tunnelID, token string) error {
	if token == "" {
		return fmt.Errorf("connector token is empty")
	}
	r.mu.Lock()
	if existing, ok := r.procs[tunnelID]; ok && existing.cmd != nil && existing.cmd.Process != nil {
		r.mu.Unlock()
		return fmt.Errorf("tunnel %s already running (pid %d)", tunnelID, existing.cmd.Process.Pid)
	}
	p := &runningProc{
		tunnelID: tunnelID,
		stopCh:   make(chan struct{}),
		logTail:  newRingBuffer(64), // last 64 log lines
	}
	r.procs[tunnelID] = p
	r.mu.Unlock()

	return r.spawn(p, token)
}

// spawn launches the process and starts the monitor goroutine.
func (r *Runner) spawn(p *runningProc, token string) error {
	binary := r.binary
	if binary == "" {
		bin, err := exec.LookPath("cloudflared")
		if err != nil {
			return fmt.Errorf("cloudflared binary not found on PATH: %w", err)
		}
		binary = bin
	}

	cmd := execCommandFn(binary, "tunnel", "--no-autoupdate", "run", "--token", token)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cloudflared: %w", err)
	}

	r.mu.Lock()
	p.cmd = cmd
	p.startedAt = time.Now()
	r.mu.Unlock()

	if r.logger != nil {
		r.logger.Info("cloudflared started", "tunnel_id", p.tunnelID, "pid", cmd.Process.Pid)
	}

	// Drain output into ring buffer (and to logger as Debug).
	go drainOutput(stdout, p.logTail)
	go drainOutput(stderr, p.logTail)

	// Monitor process — auto-restart on unclean exit unless Stop() was called.
	go r.monitor(p, token)
	return nil
}

func (r *Runner) monitor(p *runningProc, token string) {
	defer func() {
		if rec := recover(); rec != nil && r.logger != nil {
			r.logger.Error("cloudflared monitor panic", "tunnel_id", p.tunnelID, "panic", rec)
		}
	}()
	if p.cmd == nil {
		return
	}
	waitErr := p.cmd.Wait()

	select {
	case <-p.stopCh:
		// Graceful stop requested.
		r.mu.Lock()
		p.cmd = nil
		r.mu.Unlock()
		return
	default:
	}

	r.mu.Lock()
	p.cmd = nil
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Warn("cloudflared exited unexpectedly", "tunnel_id", p.tunnelID, "err", errString(waitErr))
	}

	// Backoff before restart, allowing Stop() to break out.
	backoff := time.NewTimer(2 * time.Second)
	select {
	case <-p.stopCh:
		backoff.Stop()
		return
	case <-backoff.C:
	}
	if err := r.spawn(p, token); err != nil && r.logger != nil {
		r.logger.Error("cloudflared restart failed", "tunnel_id", p.tunnelID, "err", err.Error())
	}
}

// Stop kills the cloudflared process for a tunnel and prevents auto-restart.
// Returns nil if the tunnel was not running.
func (r *Runner) Stop(tunnelID string) error {
	r.mu.Lock()
	p, ok := r.procs[tunnelID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	close(p.stopCh)
	p.stopCh = make(chan struct{}) // reset so Start can run again
	cmd := p.cmd
	r.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill cloudflared: %w", err)
	}
	return nil
}

// Forget removes a tunnel from the runner's tracking (after a Delete).
func (r *Runner) Forget(tunnelID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.procs, tunnelID)
}

// --- helpers ---

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func drainOutput(rc io.ReadCloser, ring *ringBuffer) {
	defer rc.Close()
	buf := make([]byte, 4096)
	for {
		n, err := rc.Read(buf)
		if n > 0 && ring != nil {
			ring.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}
