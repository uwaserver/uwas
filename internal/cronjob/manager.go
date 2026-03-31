// Package cronjob manages per-domain cron jobs.
package cronjob

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var (
	execCommandFn = exec.Command
	runtimeGOOS   = runtime.GOOS
)

// Job represents a cron job entry.
type Job struct {
	Schedule string `json:"schedule"` // cron expression: "*/5 * * * *"
	Command  string `json:"command"`
	Domain   string `json:"domain,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

const uwasMarker = "# UWAS managed"

// List returns all UWAS-managed cron jobs.
func List() ([]Job, error) {
	if runtimeGOOS == "windows" {
		return nil, nil
	}
	out, err := execCommandFn("crontab", "-l").Output()
	if err != nil {
		return nil, nil // no crontab
	}

	var jobs []Job
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, uwasMarker) {
			continue
		}
		// Next line is the actual job
		if i+1 < len(lines) {
			job := parseCronLine(lines[i+1])
			// Extract domain from comment
			if strings.Contains(line, "[") {
				parts := strings.SplitN(line, "[", 2)
				if len(parts) == 2 {
					job.Domain = strings.TrimRight(parts[1], "]")
				}
			}
			job.Comment = strings.TrimPrefix(line, uwasMarker+" ")
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

// Add adds a cron job.
func Add(job Job) error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("cron not supported on Windows")
	}

	// Reject newlines/carriage returns to prevent crontab injection.
	if strings.ContainsAny(job.Schedule, "\n\r") || strings.ContainsAny(job.Command, "\n\r") ||
		strings.ContainsAny(job.Domain, "\n\r") || strings.ContainsAny(job.Comment, "\n\r") {
		return fmt.Errorf("cron fields must not contain newlines")
	}

	existing, _ := execCommandFn("crontab", "-l").Output()
	comment := fmt.Sprintf("%s [%s] %s", uwasMarker, job.Domain, job.Comment)
	entry := fmt.Sprintf("%s\n%s %s\n", comment, job.Schedule, job.Command)

	newCrontab := string(existing) + entry
	return writeCrontab(newCrontab)
}

// Remove removes a cron job by matching schedule + command.
func Remove(schedule, command string) error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("cron not supported on Windows")
	}

	existing, _ := execCommandFn("crontab", "-l").Output()
	lines := strings.Split(string(existing), "\n")
	var filtered []string
	skipNext := false

	for _, line := range lines {
		if skipNext {
			skipNext = false
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, uwasMarker) {
			// Check if next line matches
			skipNext = true
			continue
		}
		// Also skip the actual job line if it matches
		if strings.Contains(trimmed, command) && strings.HasPrefix(trimmed, schedule) {
			continue
		}
		filtered = append(filtered, line)
	}

	return writeCrontab(strings.Join(filtered, "\n"))
}

func writeCrontab(content string) error {
	tmp, err := os.CreateTemp("", "uwas-crontab-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write crontab temp file: %w", err)
	}
	tmp.Close()
	return execCommandFn("crontab", tmp.Name()).Run()
}

func parseCronLine(line string) Job {
	line = strings.TrimSpace(line)
	parts := strings.Fields(line)
	if len(parts) < 6 {
		return Job{Command: line}
	}
	return Job{
		Schedule: strings.Join(parts[:5], " "),
		Command:  strings.Join(parts[5:], " "),
	}
}
