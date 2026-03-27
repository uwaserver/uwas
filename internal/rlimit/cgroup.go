// Package rlimit manages per-domain resource limits using Linux cgroups v2.
// On non-Linux platforms, all operations are no-ops.
package rlimit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const cgroupBase = "/sys/fs/cgroup/uwas"

// Limits defines resource constraints for a domain.
type Limits struct {
	// CPUPercent is the max CPU usage as a percentage (e.g. 50 = 50% of one core).
	// 0 means unlimited.
	CPUPercent int `yaml:"cpu_percent,omitempty" json:"cpu_percent,omitempty"`
	// MemoryMB is the max memory in megabytes. 0 means unlimited.
	MemoryMB int `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`
	// PIDMax is the max number of processes. 0 means unlimited.
	PIDMax int `yaml:"pid_max,omitempty" json:"pid_max,omitempty"`
}

// Apply creates/updates a cgroup for the given domain with the specified limits.
// Returns the cgroup path so a process can be assigned to it.
// No-op on non-Linux.
func Apply(domain string, limits Limits) (cgroupPath string, err error) {
	if runtime.GOOS != "linux" {
		return "", nil
	}
	if limits.CPUPercent == 0 && limits.MemoryMB == 0 && limits.PIDMax == 0 {
		return "", nil // no limits configured
	}

	path := filepath.Join(cgroupBase, sanitizeDomain(domain))
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("create cgroup %s: %w", path, err)
	}

	// CPU limit: cpu.max = "$QUOTA 100000"
	// CPUPercent 50 means 50000 out of 100000 period
	if limits.CPUPercent > 0 {
		quota := limits.CPUPercent * 1000 // percent → microseconds per 100ms period
		val := fmt.Sprintf("%d 100000", quota)
		if err := os.WriteFile(filepath.Join(path, "cpu.max"), []byte(val), 0644); err != nil {
			return path, fmt.Errorf("set cpu.max: %w", err)
		}
	}

	// Memory limit: memory.max in bytes
	if limits.MemoryMB > 0 {
		val := strconv.FormatInt(int64(limits.MemoryMB)*1024*1024, 10)
		if err := os.WriteFile(filepath.Join(path, "memory.max"), []byte(val), 0644); err != nil {
			return path, fmt.Errorf("set memory.max: %w", err)
		}
	}

	// PID limit: pids.max
	if limits.PIDMax > 0 {
		val := strconv.Itoa(limits.PIDMax)
		if err := os.WriteFile(filepath.Join(path, "pids.max"), []byte(val), 0644); err != nil {
			return path, fmt.Errorf("set pids.max: %w", err)
		}
	}

	return path, nil
}

// AssignPID moves a process into the domain's cgroup.
func AssignPID(cgroupPath string, pid int) error {
	if runtime.GOOS != "linux" || cgroupPath == "" {
		return nil
	}
	procsFile := filepath.Join(cgroupPath, "cgroup.procs")
	return os.WriteFile(procsFile, []byte(strconv.Itoa(pid)), 0644)
}

// Remove deletes the cgroup for a domain.
func Remove(domain string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	path := filepath.Join(cgroupBase, sanitizeDomain(domain))
	return os.Remove(path) // rmdir — only works if empty (no processes)
}

// sanitizeDomain converts a domain name to a safe cgroup directory name.
func sanitizeDomain(domain string) string {
	safe := make([]byte, 0, len(domain))
	for _, c := range []byte(domain) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			safe = append(safe, c)
		} else if c >= 'A' && c <= 'Z' {
			safe = append(safe, c+32) // lowercase
		} else {
			safe = append(safe, '_')
		}
	}
	return string(safe)
}
