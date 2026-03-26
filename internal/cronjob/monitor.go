package cronjob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	monitorExecCommandFn = exec.Command
	monitorRuntimeGOOS   = runtime.GOOS
)

// ExecutionRecord tracks a single cron job execution.
type ExecutionRecord struct {
	ID        string        `json:"id"`
	Domain    string        `json:"domain"`
	Command   string        `json:"command"`
	Schedule  string        `json:"schedule"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Duration  time.Duration `json:"duration"`
	ExitCode  int           `json:"exit_code"`
	Success   bool          `json:"success"`
	Output    string        `json:"output"`
	Error     string        `json:"error,omitempty"`
}

// JobStatus represents the current status of a cron job.
type JobStatus struct {
	Domain          string            `json:"domain"`
	Command         string            `json:"command"`
	Schedule        string            `json:"schedule"`
	LastRun         *ExecutionRecord  `json:"last_run,omitempty"`
	LastSuccess     *ExecutionRecord  `json:"last_success,omitempty"`
	LastFailure     *ExecutionRecord  `json:"last_failure,omitempty"`
	SuccessCount    int               `json:"success_count"`
	FailureCount    int               `json:"failure_count"`
	ConsecutiveFail int               `json:"consecutive_fail"`
	History         []ExecutionRecord `json:"history"`
}

// Monitor tracks cron job executions and provides alerting.
type Monitor struct {
	mu        sync.RWMutex
	history   map[string][]ExecutionRecord // key: domain:command
	maxHistory int
	alertFn   func(domain, command, output string, exitCode int)
	dataDir   string
}

// NewMonitor creates a new cron job monitor.
func NewMonitor(dataDir string) *Monitor {
	m := &Monitor{
		history:    make(map[string][]ExecutionRecord),
		maxHistory: 100, // Keep last 100 executions per job
		dataDir:    dataDir,
	}
	m.loadHistory()
	return m
}

// RecordExecution records a cron job execution result.
func (m *Monitor) RecordExecution(record ExecutionRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := m.jobKey(record.Domain, record.Command)
	m.history[key] = append(m.history[key], record)

	// Trim history if too large
	if len(m.history[key]) > m.maxHistory {
		m.history[key] = m.history[key][len(m.history[key])-m.maxHistory:]
	}

	// Persist to disk
	m.saveHistory()
}

// Execute runs a cron job and records the result.
func (m *Monitor) Execute(domain, schedule, command string) ExecutionRecord {
	record := ExecutionRecord{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Domain:    domain,
		Command:   command,
		Schedule:  schedule,
		StartedAt: time.Now(),
	}

	// Execute the command
	var cmd *exec.Cmd
	if monitorRuntimeGOOS == "windows" {
		cmd = monitorExecCommandFn("cmd", "/c", command)
	} else {
		cmd = monitorExecCommandFn("sh", "-c", command)
	}

	// Set working directory to domain root if possible
	if domain != "" && m.dataDir != "" {
		domainRoot := filepath.Join(m.dataDir, "..", "domains", domain)
		if info, err := os.Stat(domainRoot); err == nil && info.IsDir() {
			cmd.Dir = domainRoot
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	record.EndedAt = time.Now()
	record.Duration = record.EndedAt.Sub(record.StartedAt)

	if cmd.ProcessState != nil {
		record.ExitCode = cmd.ProcessState.ExitCode()
	}

	record.Output = stdout.String()
	if stderr.Len() > 0 {
		record.Error = stderr.String()
	}

	if err != nil {
		record.Success = false
		record.Error = err.Error()
	} else {
		record.Success = true
	}

	m.RecordExecution(record)

	// Trigger alert on failure
	if !record.Success && m.alertFn != nil {
		m.alertFn(domain, command, record.Output+record.Error, record.ExitCode)
	}

	return record
}

// GetStatus returns the current status for a specific job.
func (m *Monitor) GetStatus(domain, command string) *JobStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := m.jobKey(domain, command)
	history := m.history[key]
	if len(history) == 0 {
		return nil
	}

	status := &JobStatus{
		Domain:   domain,
		Command:  command,
		Schedule: history[len(history)-1].Schedule,
		History:  make([]ExecutionRecord, len(history)),
	}
	copy(status.History, history)

	// Calculate stats
	for i := len(history) - 1; i >= 0; i-- {
		rec := history[i]
		if status.LastRun == nil {
			status.LastRun = &rec
		}
		if rec.Success {
			status.SuccessCount++
			if status.LastSuccess == nil {
				status.LastSuccess = &rec
			}
			if status.LastFailure != nil {
				break // Stop counting consecutive failures
			}
		} else {
			status.FailureCount++
			if status.LastFailure == nil {
				status.LastFailure = &rec
			}
			status.ConsecutiveFail++
		}
	}

	return status
}

// GetAllStatus returns status for all jobs.
func (m *Monitor) GetAllStatus() []JobStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var statuses []JobStatus
	for key := range m.history {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		domain := parts[0]
		command := parts[1]
		if status := m.getStatusUnsafe(domain, command); status != nil {
			statuses = append(statuses, *status)
		}
	}
	return statuses
}

// GetDomainStatus returns all job statuses for a domain.
func (m *Monitor) GetDomainStatus(domain string) []JobStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var statuses []JobStatus
	for key, history := range m.history {
		if !strings.HasPrefix(key, domain+":") {
			continue
		}
		if len(history) == 0 {
			continue
		}
		command := strings.TrimPrefix(key, domain+":")
		if status := m.getStatusUnsafe(domain, command); status != nil {
			statuses = append(statuses, *status)
		}
	}
	return statuses
}

func (m *Monitor) getStatusUnsafe(domain, command string) *JobStatus {
	key := m.jobKey(domain, command)
	history := m.history[key]
	if len(history) == 0 {
		return nil
	}

	status := &JobStatus{
		Domain:   domain,
		Command:  command,
		Schedule: history[len(history)-1].Schedule,
		History:  make([]ExecutionRecord, 0, 10), // Last 10 for summary
	}

	// Only include last 10 in the summary
	start := 0
	if len(history) > 10 {
		start = len(history) - 10
	}
	for i := start; i < len(history); i++ {
		status.History = append(status.History, history[i])
	}

	// Calculate stats from full history
	for i := len(history) - 1; i >= 0; i-- {
		rec := history[i]
		if status.LastRun == nil {
			status.LastRun = &rec
		}
		if rec.Success {
			status.SuccessCount++
			if status.LastSuccess == nil {
				status.LastSuccess = &rec
			}
			if status.LastFailure != nil {
				break
			}
		} else {
			status.FailureCount++
			if status.LastFailure == nil {
				status.LastFailure = &rec
			}
			status.ConsecutiveFail++
		}
	}

	return status
}

// SetAlertFunc sets the alert callback for failed jobs.
func (m *Monitor) SetAlertFunc(fn func(domain, command, output string, exitCode int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alertFn = fn
}

// ClearHistory clears execution history for a job.
func (m *Monitor) ClearHistory(domain, command string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := m.jobKey(domain, command)
	delete(m.history, key)
	m.saveHistory()
}

func (m *Monitor) jobKey(domain, command string) string {
	return domain + ":" + command
}

func (m *Monitor) historyFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "cron_history.json")
}

func (m *Monitor) loadHistory() {
	file := m.historyFile()
	if file == "" {
		return
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return
	}

	var history map[string][]ExecutionRecord
	if err := json.Unmarshal(data, &history); err != nil {
		return
	}

	m.history = history
}

func (m *Monitor) saveHistory() {
	file := m.historyFile()
	if file == "" {
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(file)
	os.MkdirAll(dir, 0755)

	data, _ := json.Marshal(m.history)
	os.WriteFile(file, data, 0644)
}

// WrapCommand wraps a cron command to be monitored.
// This should be used when setting up the crontab entry.
func (m *Monitor) WrapCommand(domain, schedule, command string) string {
	// Create a wrapper that calls the UWAS cron executor
	// The wrapper will record execution via the API
	return fmt.Sprintf("curl -s -X POST http://localhost:8080/api/v1/cron/execute \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"domain\":%q,\"schedule\":%q,\"command\":%q}' 2>&1 || %s",
		domain, schedule, command, command)
}
