// Package cronjob manages per-domain cron jobs.
package cronjob

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var (
	execCommandFn = exec.Command
	runtimeGOOS   = runtime.GOOS

	// crontabTimeout bounds every crontab invocation.
	//
	// These run inside an admin HTTP handler and had no deadline. crontab can
	// block — waiting on a lock, on a filesystem that is not answering, or on
	// a host where installing a crontab needs a permission the process does
	// not have — and a blocked call held the request open with nothing to
	// interrupt it. It also hung the test suite for the full 10 minute
	// package timeout on any machine where that happens.
	crontabTimeout = 10 * time.Second
)

// runCrontab runs a crontab invocation with a deadline, killing it if it
// overruns. It reports the timeout as an error rather than waiting.
//
// Start is called on this goroutine on purpose. Running cmd.Run in a
// goroutine and reaching for cmd.Process from here races: Run sets Process
// while this side reads it. After Start returns, Process is set and stable,
// and killing it while Wait runs is the documented pattern.
//
// stderr is captured and attached to an ExitError the way cmd.Output does,
// because readCrontab tells "no crontab for user" — an empty crontab, not a
// failure — from a real error by reading it.
func runCrontab(cmd *exec.Cmd, capture bool) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	if capture {
		cmd.Stdout = &stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = &stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(crontabTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		select { case <-timer.C: default: }
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) == 0 {
			ee.Stderr = stderr.Bytes()
		}
		return stdout.Bytes(), err
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done // reap the killed process
		return nil, fmt.Errorf("crontab did not respond within %s", crontabTimeout)
	}
}

// Job represents a cron job entry.
type Job struct {
	Schedule string `json:"schedule"` // cron expression: "*/5 * * * *"
	Command  string `json:"command"`
	Domain   string `json:"domain,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

const uwasMarker = "# UWAS managed"

// readCrontab returns the user's current crontab. A genuinely empty crontab
// (`crontab -l` exits non-zero with a "no crontab for ..." message) is reported
// as ("", nil). Any other failure returns an error so write callers ABORT
// instead of treating a transient `crontab -l` failure as "no jobs" and then
// overwriting an existing crontab with only their own entry — which would
// destroy every unrelated cron job on the system.
func readCrontab() (string, error) {
	out, err := runCrontab(execCommandFn("crontab", "-l"), true)
	if err == nil {
		return string(out), nil
	}
	var stderr string
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = string(ee.Stderr)
	}
	if strings.Contains(strings.ToLower(stderr), "no crontab") {
		return "", nil
	}
	return "", fmt.Errorf("read crontab: %w: %s", err, strings.TrimSpace(stderr))
}

// List returns all UWAS-managed cron jobs.
func List() ([]Job, error) {
	if runtimeGOOS == "windows" {
		return nil, nil
	}
	out, err := runCrontab(execCommandFn("crontab", "-l"), true)
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
	// Enforce the same shell metacharacter restriction that Monitor.Execute
	// applies at execution time. Without this, a malicious admin can inject
	// cron jobs with $(...), `...`, |, >, ;, etc., and those commands will
	// execute when cron fires — bypassing the per-execution validation.
	if err := validateShellCommand(job.Command); err != nil {
		return err
	}

	existing, err := readCrontab()
	if err != nil {
		return err
	}
	// Prevent duplicate entries: if a job with the same schedule and command
	// already exists in the crontab, skip the write. Without this, two calls
	// to Add() with identical schedule+command would write two crontab lines,
	// and cron would fire both on every matching tick — doubling side-effects.
	// The Monitor.Execute overlap guard only prevents concurrent calls from the
	// same goroutine, not two crontab entries from firing simultaneously.
	for _, line := range strings.Split(existing, "\n") {
		// Skip empty lines and UWAS comment lines; only parse actual job lines.
		// Inverting the marker check: comment lines (which contain uwasMarker) must
		// be skipped — they have 6+ fields and parseCronLine returns
		// Schedule="#UWAS managed [domain]" which never matches a real cron schedule,
		// silently breaking deduplication for all UWAS-managed jobs.
		if strings.TrimSpace(line) == "" || strings.Contains(line, uwasMarker) {
			continue
		}
		parsed := parseCronLine(line)
		if parsed.Schedule == job.Schedule && parsed.Command == job.Command {
			return fmt.Errorf("cron job already exists: schedule=%s command=%s", job.Schedule, job.Command)
		}
	}
	comment := fmt.Sprintf("%s [%s] %s", uwasMarker, job.Domain, job.Comment)
	entry := fmt.Sprintf("%s\n%s %s\n", comment, job.Schedule, job.Command)

	newCrontab := existing + entry
	return writeCrontab(newCrontab)
}

// Remove removes a cron job by matching schedule + command.
func Remove(schedule, command string) error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("cron not supported on Windows")
	}

	existing, err := readCrontab()
	if err != nil {
		return err
	}
	lines := strings.Split(existing, "\n")
	var filtered []string

	for i := 0; i < len(lines); i++ {
		// A UWAS job is a marker comment line followed by its job line. Only
		// remove the pair whose job line actually matches schedule+command —
		// previously the marker caused the next line to be dropped
		// unconditionally, deleting EVERY UWAS-managed cron job.
		if strings.Contains(strings.TrimSpace(lines[i]), uwasMarker) && i+1 < len(lines) {
			j := parseCronLine(lines[i+1])
			if j.Schedule == schedule && j.Command == command {
				i++ // skip both the comment and the matching job line
				continue
			}
		}
		filtered = append(filtered, lines[i])
	}

	return writeCrontab(strings.Join(filtered, "\n"))
}

// RemoveByDomain removes all UWAS-managed cron jobs for a given domain.
func RemoveByDomain(domain string) error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("cron not supported on Windows")
	}
	existing, err := readCrontab()
	if err != nil {
		return err
	}
	lines := strings.Split(existing, "\n")
	var filtered []string
	skipNext := false
	marker := fmt.Sprintf("%s [%s]", uwasMarker, domain)

	for _, line := range lines {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.Contains(line, marker) {
			skipNext = true // skip the comment line and the next job line
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
	_, err = runCrontab(execCommandFn("crontab", tmp.Name()), false)
	return err
}

func parseCronLine(line string) Job {
	line = strings.TrimSpace(line)
	parts := strings.Fields(line)
	// Cron shorthand schedules (@reboot, @daily, @hourly, …) are a single
	// field followed by the command, not the 5-field numeric schedule. Without
	// this, such a job parses with an empty Schedule and the whole "@reboot …"
	// merged into Command — making it unmatchable by List/Remove (it could
	// never be deleted through the API).
	if len(parts) >= 2 && strings.HasPrefix(parts[0], "@") {
		return Job{
			Schedule: parts[0],
			Command:  strings.Join(parts[1:], " "),
		}
	}
	if len(parts) < 6 {
		return Job{Command: line}
	}
	return Job{
		Schedule: strings.Join(parts[:5], " "),
		Command:  strings.Join(parts[5:], " "),
	}
}
